// Package mapview reads the map of a case from its database and shows it to a person: as a
// tree for the terminal, or as JSON. It only reads; nothing here starts a command or calls a
// model.
package mapview

import (
	"context"
	"time"

	"github.com/sozureke/kagegami/internal/secret"
	"github.com/sozureke/kagegami/internal/store"
)

// View is the whole map of a case.
type View struct {
	Hosts       []Host       `json:"hosts"`
	Findings    []Finding    `json:"findings"`
	Credentials []Credential `json:"credentials"`
	// Notes are the remarks the agent and the parsers made (script output, observations).
	Notes []Note `json:"notes"`
	// Usage is what the models have used on this case, by tier and model.
	Usage []Usage `json:"usage"`
}

// Usage is the use of one model in one tier.
type Usage struct {
	Tier             string `json:"tier"`
	Model            string `json:"model"`
	Calls            int    `json:"calls"`
	Failed           int    `json:"failed"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

// Host is a host with its services.
type Host struct {
	IP        string    `json:"ip"`
	Hostname  string    `json:"hostname,omitempty"`
	OS        string    `json:"os,omitempty"`
	Status    string    `json:"status"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Services  []Service `json:"services"`
	// SourceBlobID is the command this was last updated from, if any. It is not part of the
	// view a person sees (kagegami map prints and encodes the map without it); a caller that
	// builds a prompt for a model can cite it as the evidence behind a line.
	SourceBlobID *int64 `json:"-"`
}

// Service is a port on a host with the web paths found on it.
type Service struct {
	Port         int       `json:"port"`
	Proto        string    `json:"proto"`
	State        string    `json:"state"`
	Name         string    `json:"name,omitempty"`
	Version      string    `json:"version,omitempty"`
	Paths        []WebPath `json:"paths,omitempty"`
	SourceBlobID *int64    `json:"-"`
}

// WebPath is a path found on a web service.
type WebPath struct {
	Path         string `json:"path"`
	StatusCode   *int   `json:"status_code,omitempty"`
	Note         string `json:"note,omitempty"`
	SourceBlobID *int64 `json:"-"`
}

// Finding is something worth reporting.
type Finding struct {
	Severity     string    `json:"severity"`
	Title        string    `json:"title"`
	Description  string    `json:"description,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	SourceBlobID *int64    `json:"-"`
}

// Credential is a login that was found. Its secret is a secret.String, which prints and
// encodes as a mask: this package has no way to show the real value.
type Credential struct {
	Username string        `json:"username,omitempty"`
	Secret   secret.String `json:"secret"`
	Kind     string        `json:"kind"`
	WorksOn  string        `json:"works_on,omitempty"`
}

// Note is one remark.
type Note struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

// Load reads the map from the database. Services, paths and everything else come in a stable
// order (by address, port, path; by the time it was recorded).
func Load(ctx context.Context, st *store.Store) (View, error) {
	r := st.Repo()
	v := View{Hosts: []Host{}, Findings: []Finding{}, Credentials: []Credential{}, Notes: []Note{}, Usage: []Usage{}}

	hosts, err := r.ListHosts(ctx)
	if err != nil {
		return View{}, err
	}
	for _, h := range hosts {
		host := Host{IP: h.IP, Hostname: h.Hostname, OS: h.OS, Status: h.Status, FirstSeen: h.FirstSeen, LastSeen: h.LastSeen, Services: []Service{}, SourceBlobID: h.SourceBlobID}
		svcs, err := r.ListServices(ctx, h.ID)
		if err != nil {
			return View{}, err
		}
		for _, s := range svcs {
			svc := Service{Port: s.Port, Proto: s.Proto, State: s.State, Name: s.Name, Version: s.Version, SourceBlobID: s.SourceBlobID}
			paths, err := r.ListWebPaths(ctx, s.ID)
			if err != nil {
				return View{}, err
			}
			for _, p := range paths {
				svc.Paths = append(svc.Paths, WebPath{Path: p.Path, StatusCode: p.StatusCode, Note: p.Note, SourceBlobID: p.SourceBlobID})
			}
			host.Services = append(host.Services, svc)
		}
		v.Hosts = append(v.Hosts, host)
	}

	findings, err := r.ListFindings(ctx)
	if err != nil {
		return View{}, err
	}
	for _, f := range findings {
		v.Findings = append(v.Findings, Finding{Severity: f.Severity, Title: f.Title, Description: f.Description, CreatedAt: f.CreatedAt, SourceBlobID: f.SourceBlobID})
	}

	creds, err := r.ListCredentials(ctx)
	if err != nil {
		return View{}, err
	}
	for _, c := range creds {
		v.Credentials = append(v.Credentials, Credential{Username: c.Username, Secret: c.Secret, Kind: c.Kind, WorksOn: c.WorksOn})
	}

	entries, err := r.ListJournal(ctx)
	if err != nil {
		return View{}, err
	}
	for _, e := range entries {
		if e.Author == "ai" {
			v.Notes = append(v.Notes, Note{Time: e.Time, Text: e.Text})
		}
	}

	totals, err := r.UsageTotals(ctx)
	if err != nil {
		return View{}, err
	}
	for _, t := range totals {
		v.Usage = append(v.Usage, Usage{Tier: t.Tier, Model: t.Model, Calls: t.Calls, Failed: t.Failed, PromptTokens: t.PromptTokens, CompletionTokens: t.CompletionTokens})
	}
	return v, nil
}
