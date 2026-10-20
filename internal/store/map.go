package store

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"time"
)

type Host struct {
	ID           int64
	IP           string
	Hostname     string
	OS           string
	Status       string
	FirstSeen    time.Time
	LastSeen     time.Time
	SourceBlobID *int64
}

type Service struct {
	ID           int64
	HostID       int64
	Port         int
	Proto        string
	Name         string
	Version      string
	State        string
	SourceBlobID *int64
}

type WebPath struct {
	ID           int64
	ServiceID    int64
	Path         string
	StatusCode   *int
	Note         string
	SourceBlobID *int64
}

func (r *Repo) UpsertHost(ctx context.Context, h Host) (Host, error) {
	addr, err := netip.ParseAddr(h.IP)
	if err != nil {
		return Host{}, invalid("host ip %q", h.IP)
	}
	now := toMS(r.now())

	const query = `
INSERT INTO host (ip, hostname, os, status, first_seen, last_seen, source_blob_id)
VALUES (?, ?, ?, COALESCE(NULLIF(?, ''), 'unknown'), ?, ?, ?)
ON CONFLICT(ip) DO UPDATE SET
    hostname       = COALESCE(NULLIF(excluded.hostname, ''), host.hostname),
    os             = COALESCE(NULLIF(excluded.os, ''), host.os),
    status         = CASE WHEN excluded.status = 'unknown' THEN host.status ELSE excluded.status END,
    last_seen      = excluded.last_seen,
    source_blob_id = COALESCE(excluded.source_blob_id, host.source_blob_id)
RETURNING id, ip, hostname, os, status, first_seen, last_seen, source_blob_id`

	row := r.q.QueryRowContext(ctx, query, addr.String(), h.Hostname, h.OS, h.Status, now, now, h.SourceBlobID)
	return scanHost(row)
}

func (r *Repo) GetHostByIP(ctx context.Context, ip string) (Host, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Host{}, invalid("host ip %q", ip)
	}
	return scanHost(r.q.QueryRowContext(ctx, hostSelect+" WHERE ip = ?", addr.String()))
}

// returns every host in the order it was first seen.
func (r *Repo) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := r.q.QueryContext(ctx, hostSelect+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

const hostSelect = `SELECT id, ip, hostname, os, status, first_seen, last_seen, source_blob_id FROM host`

// implemented by both *sql.Row and *sql.Rows, so one scan function serves
type scanner interface{ Scan(dest ...any) error }

func scanHost(s scanner) (Host, error) {
	var h Host
	var first, last int64
	var blob sql.NullInt64
	err := s.Scan(&h.ID, &h.IP, &h.Hostname, &h.OS, &h.Status, &first, &last, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrNotFound
	}
	if err != nil {
		return Host{}, err
	}
	h.FirstSeen, h.LastSeen = fromMS(first), fromMS(last)
	if blob.Valid {
		h.SourceBlobID = &blob.Int64
	}
	return h, nil
}

func (r *Repo) UpsertService(ctx context.Context, s Service) (Service, error) {
	if s.Port < 1 || s.Port > 65535 {
		return Service{}, invalid("port %d", s.Port)
	}
	if s.Proto != "tcp" && s.Proto != "udp" {
		return Service{}, invalid("protocol %q", s.Proto)
	}

	const query = `
INSERT INTO service (host_id, port, proto, name, version, state, source_blob_id)
VALUES (?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), 'open'), ?)
ON CONFLICT(host_id, port, proto) DO UPDATE SET
    name           = COALESCE(NULLIF(excluded.name, ''), service.name),
    version        = COALESCE(NULLIF(excluded.version, ''), service.version),
    state          = excluded.state,
    source_blob_id = COALESCE(excluded.source_blob_id, service.source_blob_id)
RETURNING id, host_id, port, proto, name, version, state, source_blob_id`

	row := r.q.QueryRowContext(ctx, query, s.HostID, s.Port, s.Proto, s.Name, s.Version, s.State, s.SourceBlobID)
	return scanService(row)
}

// returns the services of a host ordered by port.
func (r *Repo) ListServices(ctx context.Context, hostID int64) ([]Service, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, host_id, port, proto, name, version, state, source_blob_id
		FROM service WHERE host_id = ? ORDER BY port, proto`, hostID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Service
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanService(s scanner) (Service, error) {
	var v Service
	var blob sql.NullInt64
	err := s.Scan(&v.ID, &v.HostID, &v.Port, &v.Proto, &v.Name, &v.Version, &v.State, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	if err != nil {
		return Service{}, err
	}
	if blob.Valid {
		v.SourceBlobID = &blob.Int64
	}
	return v, nil
}

func (r *Repo) UpsertWebPath(ctx context.Context, w WebPath) (WebPath, error) {
	if w.Path == "" || w.Path[0] != '/' {
		return WebPath{}, invalid("web path %q must start with /", w.Path)
	}

	const query = `
INSERT INTO web_path (service_id, path, status_code, note, source_blob_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(service_id, path) DO UPDATE SET
    status_code    = COALESCE(excluded.status_code, web_path.status_code),
    note           = COALESCE(NULLIF(excluded.note, ''), web_path.note),
    source_blob_id = COALESCE(excluded.source_blob_id, web_path.source_blob_id)
RETURNING id, service_id, path, status_code, note, source_blob_id`

	row := r.q.QueryRowContext(ctx, query, w.ServiceID, w.Path, w.StatusCode, w.Note, w.SourceBlobID)
	return scanWebPath(row)
}

// returns the paths of a service in alphabetical order.
func (r *Repo) ListWebPaths(ctx context.Context, serviceID int64) ([]WebPath, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, service_id, path, status_code, note, source_blob_id
		FROM web_path WHERE service_id = ? ORDER BY path`, serviceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []WebPath
	for rows.Next() {
		w, err := scanWebPath(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func scanWebPath(s scanner) (WebPath, error) {
	var w WebPath
	var status, blob sql.NullInt64
	err := s.Scan(&w.ID, &w.ServiceID, &w.Path, &status, &w.Note, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return WebPath{}, ErrNotFound
	}
	if err != nil {
		return WebPath{}, err
	}
	if status.Valid {
		code := int(status.Int64)
		w.StatusCode = &code
	}
	if blob.Valid {
		w.SourceBlobID = &blob.Int64
	}
	return w, nil
}
