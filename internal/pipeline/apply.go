package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sozureke/kagegami/internal/recon"
	"github.com/sozureke/kagegami/internal/store"
)

// Added counts what a delta added to the map. Entries that were already known, and were only
// refreshed, are not counted.
type Added struct {
	Hosts, Services, WebPaths, Findings, Notes int
}

// Total is the number of new entries of every kind.
func (a Added) Total() int { return a.Hosts + a.Services + a.WebPaths + a.Findings + a.Notes }

// Apply writes a delta into the map in one transaction: either all of it is stored, or none.
// The delta is validated first. Entries that are already there are updated, not duplicated, so
// scanning the same host again refreshes the map; every entry written points to the blob it
// came from (source, which may be nil), which is how a finding is traced back to a command.
//
// A host or a service that a later entry refers to is created if the delta did not mention it
// (a path found by a directory scan belongs to a service the scan did not report). A service
// that is only created for that reason does not overwrite what is known about the one that exists.
func Apply(ctx context.Context, st *store.Store, d recon.StateDelta, source *int64) (Added, error) {
	d, err := d.Validate()
	if err != nil {
		return Added{}, err
	}
	var added Added
	err = st.InTx(ctx, func(r *store.Repo) error {
		added = Added{} // the function may run once, but a retry must not double count
		a := applier{ctx: ctx, repo: r, source: source, hosts: map[string]store.Host{}}
		return a.run(d, &added)
	})
	if err != nil {
		return Added{}, err
	}
	return added, nil
}

type applier struct {
	ctx    context.Context
	repo   *store.Repo
	source *int64
	hosts  map[string]store.Host // by address, for the hosts met so far
}

func (a *applier) run(d recon.StateDelta, added *Added) error {
	for _, h := range d.Hosts {
		isNew, err := a.isNewHost(h.IP)
		if err != nil {
			return err
		}
		row, err := a.repo.UpsertHost(a.ctx, store.Host{IP: h.IP, Hostname: h.Hostname, OS: h.OS, Status: h.Status, SourceBlobID: a.source})
		if err != nil {
			return fmt.Errorf("store host %s: %w", h.IP, err)
		}
		a.hosts[h.IP] = row
		if isNew {
			added.Hosts++
		}
	}
	for _, s := range d.Services {
		host, newHost, err := a.host(s.HostIP)
		if err != nil {
			return err
		}
		if newHost {
			added.Hosts++
		}
		existing, found, err := a.findService(host.ID, s.Port, s.Proto)
		if err != nil {
			return err
		}
		if s.State == "" && found {
			s.State = existing.State // no news about the state: keep what is known
		}
		if found && s.Version == "" && existing.Version != "" {
			// A scan without version detection names a service after its port (http-alt for
			// 8000); that must not replace what a version scan found out.
			s.Name = existing.Name
		}
		if _, err := a.repo.UpsertService(a.ctx, store.Service{
			HostID: host.ID, Port: s.Port, Proto: s.Proto, Name: s.Name, Version: s.Version, State: s.State, SourceBlobID: a.source,
		}); err != nil {
			return fmt.Errorf("store service %s %d/%s: %w", s.HostIP, s.Port, s.Proto, err)
		}
		if !found {
			added.Services++
		}
	}
	var unresolved []recon.WebPathDelta
	for _, w := range d.WebPaths {
		if w.HostIP == "" {
			ip, ok, err := a.hostByName(w.Host)
			if err != nil {
				return err
			}
			if !ok {
				unresolved = append(unresolved, w)
				continue
			}
			w.HostIP = ip
		}
		host, newHost, err := a.host(w.HostIP)
		if err != nil {
			return err
		}
		if newHost {
			added.Hosts++
		}
		svc, found, err := a.findService(host.ID, w.Port, w.Proto)
		if err != nil {
			return err
		}
		if !found {
			svc, err = a.repo.UpsertService(a.ctx, store.Service{HostID: host.ID, Port: w.Port, Proto: w.Proto, SourceBlobID: a.source})
			if err != nil {
				return fmt.Errorf("store service %s %d/%s: %w", w.HostIP, w.Port, w.Proto, err)
			}
			added.Services++
		}
		known, err := a.hasPath(svc.ID, w.Path)
		if err != nil {
			return err
		}
		if _, err := a.repo.UpsertWebPath(a.ctx, store.WebPath{ServiceID: svc.ID, Path: w.Path, StatusCode: w.StatusCode, Note: w.Note, SourceBlobID: a.source}); err != nil {
			return fmt.Errorf("store path %s: %w", w.Path, err)
		}
		if !known {
			added.WebPaths++
		}
	}
	if err := a.findings(d.Findings, added); err != nil {
		return err
	}
	return a.notes(append(slices.Clone(d.Notes), unresolvedNotes(unresolved)...), added)
}

// hostByName finds the address of the host the map knows by this name.
func (a *applier) hostByName(name string) (string, bool, error) {
	hosts, err := a.repo.ListHosts(a.ctx)
	if err != nil {
		return "", false, err
	}
	for _, h := range hosts {
		if strings.EqualFold(h.Hostname, name) {
			return h.IP, true, nil
		}
	}
	return "", false, nil
}

// unresolvedNotes turns paths found on a host that the map does not know by name into notes,
// one per service: they cannot be placed on the map, but they must not be lost.
func unresolvedNotes(paths []recon.WebPathDelta) []recon.NoteDelta {
	type key struct {
		host  string
		port  int
		proto string
	}
	var order []key
	byService := map[key][]string{}
	for _, w := range paths {
		k := key{w.Host, w.Port, w.Proto}
		if _, ok := byService[k]; !ok {
			order = append(order, k)
		}
		entry := w.Path
		if w.StatusCode != nil {
			entry += fmt.Sprintf(" (%d)", *w.StatusCode)
		}
		byService[k] = append(byService[k], entry)
	}
	var notes []recon.NoteDelta
	for _, k := range order {
		text := fmt.Sprintf("paths found on %s:%d/%s (address not known): %s", k.host, k.port, k.proto, strings.Join(byService[k], ", "))
		if len(text) > 4000 {
			text = text[:4000] + "..."
		}
		notes = append(notes, recon.NoteDelta{Text: text})
	}
	return notes
}

func (a *applier) isNewHost(ip string) (bool, error) {
	_, err := a.repo.GetHostByIP(a.ctx, ip)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	return false, err
}

// host returns the host with the address, creating it if the map does not know it yet.
func (a *applier) host(ip string) (h store.Host, created bool, err error) {
	if h, ok := a.hosts[ip]; ok {
		return h, false, nil
	}
	created, err = a.isNewHost(ip)
	if err != nil {
		return store.Host{}, false, err
	}
	h, err = a.repo.UpsertHost(a.ctx, store.Host{IP: ip, SourceBlobID: a.source})
	if err != nil {
		return store.Host{}, false, fmt.Errorf("store host %s: %w", ip, err)
	}
	a.hosts[ip] = h
	return h, created, nil
}

func (a *applier) findService(hostID int64, port int, proto string) (store.Service, bool, error) {
	list, err := a.repo.ListServices(a.ctx, hostID)
	if err != nil {
		return store.Service{}, false, err
	}
	for _, s := range list {
		if s.Port == port && s.Proto == proto {
			return s, true, nil
		}
	}
	return store.Service{}, false, nil
}

func (a *applier) hasPath(serviceID int64, path string) (bool, error) {
	list, err := a.repo.ListWebPaths(a.ctx, serviceID)
	if err != nil {
		return false, err
	}
	for _, w := range list {
		if w.Path == path {
			return true, nil
		}
	}
	return false, nil
}

// findings adds the findings that are not on record yet (same severity and title).
func (a *applier) findings(list []recon.FindingDelta, added *Added) error {
	if len(list) == 0 {
		return nil
	}
	existing, err := a.repo.ListFindings(a.ctx)
	if err != nil {
		return err
	}
	known := map[[2]string]bool{}
	for _, f := range existing {
		known[[2]string{f.Severity, f.Title}] = true
	}
	for _, f := range list {
		key := [2]string{f.Severity, f.Title}
		if known[key] {
			continue
		}
		if _, err := a.repo.AddFinding(a.ctx, store.Finding{Severity: f.Severity, Title: f.Title, Description: f.Description, SourceBlobID: a.source}); err != nil {
			return fmt.Errorf("store finding %q: %w", f.Title, err)
		}
		known[key] = true
		added.Findings++
	}
	return nil
}

// notes adds the notes to the journal as the agent's, unless the same text is already there.
func (a *applier) notes(list []recon.NoteDelta, added *Added) error {
	if len(list) == 0 {
		return nil
	}
	existing, err := a.repo.ListJournal(a.ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, e := range existing {
		if e.Author == "ai" {
			known[e.Text] = true
		}
	}
	for _, n := range list {
		if known[n.Text] {
			continue
		}
		if _, err := a.repo.AddJournal(a.ctx, store.JournalEntry{Author: "ai", Text: n.Text, SourceBlobID: a.source}); err != nil {
			return fmt.Errorf("store note: %w", err)
		}
		known[n.Text] = true
		added.Notes++
	}
	return nil
}
