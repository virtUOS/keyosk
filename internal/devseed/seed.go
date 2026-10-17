package devseed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/e2etest"
	"github.com/virtuos/keyosk/internal/keyprovider"
	"github.com/virtuos/keyosk/internal/litellm"
)

// Manifest describes a seeded instance: where it runs, how its users are
// named in LiteLLM, and who is in it.
type Manifest struct {
	URL    string         `json:"url"`
	Prefix string         `json:"prefix"`
	Users  []ManifestUser `json:"users"`
}

// ManifestUser is a persona as seeded.
type ManifestUser struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Profile string `json:"profile"`
	Shows   string `json:"shows"`
}

// mockUser is an entry of dev/mock-users.json.
type mockUser struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

// Seed starts the instance and fills it with Profiles and Personas. It leaves
// the instance running.
//
// Everything a user or an admin could do goes through the app's own pages, so
// keys, limits and the audit log come out as they would in production. Only
// what nobody can do there is written to the database, with the server
// stopped: a key that has expired and a deadline that has passed. A restart
// then empties the usage cache, which would otherwise hold the "no usage" the
// key's first page load saw, and lets the expiry job apply the deadline.
func Seed(ctx context.Context, inst *Instance, out io.Writer) (*Manifest, error) {
	mock, err := readMockUsers(inst.MockUsersPath())
	if err != nil {
		return nil, err
	}

	if err := inst.Start(ctx); err != nil {
		return nil, err
	}
	db, err := database.Open(inst.DBPath())
	if err != nil {
		return nil, err
	}
	store := database.NewStore(db)
	defer store.Close()
	gw := litellm.NewClient(e2etest.BaseURL(), e2etest.MasterKey())
	s := &seeder{inst: inst, store: store, gw: gw, out: out, clients: map[string]*Client{}}

	fmt.Fprintf(out, "Seeding %s (server log: %s)\n", inst.URL(), inst.LogPath())
	steps := []struct {
		what string
		run  func(context.Context) error
	}{
		{"signing everyone in", func(ctx context.Context) error { return s.login(ctx, mock) }},
		{"creating the profiles", s.profiles},
		{"assigning them", s.assign},
		{"generating keys and sending requests", s.keys},
		{"waiting for LiteLLM to record the spend", s.waitForSpend},
		{"expiring a key and an assignment", s.backdate},
		{"marking the missing key", s.markMissing},
	}
	for _, step := range steps {
		fmt.Fprintf(out, "  %s\n", step.what)
		if err := step.run(ctx); err != nil {
			return nil, fmt.Errorf("%s: %w", step.what, err)
		}
	}

	m := &Manifest{URL: inst.URL(), Prefix: inst.Prefix}
	for _, p := range Personas {
		profile := p.Profile
		if p.UntilDays < 0 || profile == "" {
			profile = DefaultProfile
		}
		u := mock[p.Sub]
		m.Users = append(m.Users, ManifestUser{
			Sub: p.Sub, Name: u.Name, Email: u.Email, Profile: profile, Shows: p.Shows,
		})
	}
	return m, nil
}

type seeder struct {
	inst    *Instance
	store   *database.Store
	gw      *litellm.Client
	out     io.Writer
	clients map[string]*Client
	// sentAt is when the keys step began sending requests. A quota window
	// that has reset since then has nothing to show for them.
	sentAt time.Time
}

// login signs every persona in once: an account exists only from its first
// login, and the admin can assign a profile only to an account that exists.
func (s *seeder) login(ctx context.Context, mock map[string]mockUser) error {
	for _, p := range Personas {
		want, ok := mock[p.Sub]
		if !ok {
			return fmt.Errorf("%s is not in %s", p.Sub, s.inst.MockUsersPath())
		}
		c := NewClient(s.inst.URL())
		if err := c.Login(ctx, p.Sub); err != nil {
			return err
		}
		// The mock reads its users file when it starts, and accepts any
		// subject; one it does not know signs in without an address. A
		// restart does not pick up a replaced file: the file is bind-mounted
		// on its own, and the container keeps the copy it was created with.
		u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
		if err != nil {
			return err
		}
		if u.Email != want.Email {
			return fmt.Errorf("%s signed in as %q, not %q: the OIDC mock has not read the current %s; "+
				"recreate it with `docker compose -f dev/docker-compose.yml up -d --force-recreate oidc-mock` (or podman compose)",
				p.Sub, u.Email, want.Email, s.inst.MockUsersPath())
		}
		s.clients[p.Sub] = c
	}
	return nil
}

func (s *seeder) admin(ctx context.Context) (*Client, error) {
	c := s.clients["admin"]
	_, err := c.Page(ctx, "/admin")
	return c, err
}

func (s *seeder) profiles(ctx context.Context) error {
	admin, err := s.admin(ctx)
	if err != nil {
		return err
	}
	for _, p := range Profiles {
		form := url.Values{
			"name":        {p.Name},
			"description": {p.Description},
			"model":       p.Models,
		}
		if p.TPM > 0 {
			form.Set("tpm_limit", strconv.FormatInt(p.TPM, 10))
		}
		if p.RPM > 0 {
			form.Set("rpm_limit", strconv.FormatInt(p.RPM, 10))
		}
		if p.KeyDays > 0 {
			form.Set("key_duration_days", strconv.Itoa(p.KeyDays))
		}
		for _, q := range p.Quotas {
			form.Add("quota_budget", strconv.FormatFloat(q.Budget, 'f', -1, 64))
			form.Add("quota_period", q.Period)
		}
		if !p.Default {
			if err := admin.Admin(ctx, "/admin/profiles", form, "Profile created"); err != nil {
				return err
			}
			continue
		}
		def, err := s.store.GetDefaultProfile(ctx)
		if err != nil {
			return err
		}
		// The default profile must stay the default, and the form says so.
		form.Set("is_default", "on")
		if err := admin.Admin(ctx, fmt.Sprintf("/admin/profiles/%d", def.ID), form, "Profile updated"); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) profileIDs(ctx context.Context) (map[string]int64, error) {
	profiles, err := s.store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, p := range profiles {
		ids[p.Name] = p.ID
	}
	return ids, nil
}

// assign gives personas their profiles. It comes before the keys, which are
// created with their owner's limits: a key generated first would carry the
// default profile's until the next limit sync, and the requests sent with it
// would count against the wrong budget.
func (s *seeder) assign(ctx context.Context) error {
	admin, err := s.admin(ctx)
	if err != nil {
		return err
	}
	ids, err := s.profileIDs(ctx)
	if err != nil {
		return err
	}
	today := time.Now()
	for _, p := range Personas {
		if p.Profile == "" {
			continue
		}
		u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
		if err != nil {
			return err
		}
		form := url.Values{"profile_id": {strconv.FormatInt(ids[p.Profile], 10)}}
		if p.UntilDays != 0 {
			// A deadline that has passed is set for tomorrow here and moved
			// back with the server stopped: set from the panel, the running
			// server could apply it before the key is generated.
			days := max(p.UntilDays, 1)
			form.Set("expires_at", today.AddDate(0, 0, days).Format("2006-01-02"))
			if p.After != "" {
				form.Set("after_expiry", strconv.FormatInt(ids[p.After], 10))
			}
			if p.RevokeAtExpiry {
				form.Set("revoke_at_expiry", "on")
			}
		}
		if err := admin.Admin(ctx, fmt.Sprintf("/admin/users/%d/profile", u.ID), form, "User profile updated"); err != nil {
			return fmt.Errorf("%s: %w", p.Sub, err)
		}
	}
	return nil
}

func (s *seeder) keys(ctx context.Context) error {
	s.sentAt = time.Now()
	for _, p := range Personas {
		if !p.Key {
			continue
		}
		c := s.clients[p.Sub]
		secret, err := c.GenerateKey(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", p.Sub, err)
		}
		for _, model := range p.Requests {
			if err := Complete(ctx, e2etest.BaseURL(), secret, model); err != nil {
				return fmt.Errorf("%s: %w", p.Sub, err)
			}
		}
		if p.MissingKey {
			k, err := s.key(ctx, p.Sub)
			if err != nil {
				return err
			}
			// What an admin deleting it in LiteLLM's own UI would do.
			if err := s.gw.DeleteKey(ctx, k.KeyRef); err != nil {
				return fmt.Errorf("%s: delete the key in LiteLLM: %w", p.Sub, err)
			}
		}
	}
	return nil
}

func (s *seeder) key(ctx context.Context, sub string) (*database.APIKey, error) {
	u, err := s.store.GetUserByOIDCSub(ctx, sub)
	if err != nil {
		return nil, err
	}
	k, err := s.store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sub, err)
	}
	return k, nil
}

// waitForSpend waits until LiteLLM has recorded every request a persona
// sent. LiteLLM writes spend in batches, a few seconds after the requests,
// and the dashboard reads it in two places: the history behind the chart and
// the model table, and the spend counted against each quota window behind the
// bars. The wait covers both, so no page is shown before its figures are in.
func (s *seeder) waitForSpend(ctx context.Context) error {
	provider := litellm.NewProvider(s.gw)
	deadline := time.Now().Add(90 * time.Second)
	for _, p := range Personas {
		if len(p.Requests) == 0 {
			continue
		}
		k, err := s.key(ctx, p.Sub)
		if err != nil {
			return err
		}
		owner := s.inst.Prefix + ":" + p.Sub
		for {
			recorded, err := s.spendRecorded(ctx, provider, k.KeyRef, owner, len(p.Requests))
			if err != nil {
				return fmt.Errorf("%s: %w", p.Sub, err)
			}
			if recorded {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s: LiteLLM did not record all %d requests within 90s", p.Sub, len(p.Requests))
			}
			if err := sleep(ctx, time.Second); err != nil {
				return err
			}
		}
	}
	return nil
}

// spendRecorded reports whether the gateway has written all sent requests to
// the owner's history and has counted spend against every quota window of the
// key and its owner. A persona without quotas has no windows to wait for, and
// a window that rolled over since the requests were sent, as the hourly one
// does at the top of the hour, is rightly back at zero.
func (s *seeder) spendRecorded(ctx context.Context, provider *litellm.Provider, ref, owner string, sent int) (bool, error) {
	// Two days rather than one: the history is kept in UTC days, and a run
	// that crosses midnight would otherwise lose the requests it sent before.
	h, err := provider.History(ctx, owner, 2)
	if err != nil {
		return false, err
	}
	var requests int64
	for _, m := range h.Models {
		requests += m.Requests
	}
	if requests < int64(sent) {
		return false, nil
	}
	windows, err := provider.Windows(ctx, ref, owner)
	if err != nil {
		return false, err
	}
	for _, w := range windows {
		if w.Used <= 0 && !resetSince(w, s.sentAt) {
			return false, nil
		}
	}
	return true, nil
}

// resetSince reports whether w has rolled over since t. LiteLLM reports when
// a window resets next; one period before that is when it last did.
func resetSince(w keyprovider.WindowUsage, t time.Time) bool {
	var period time.Duration
	switch w.Period {
	case "1h":
		period = time.Hour
	case "24h":
		period = 24 * time.Hour
	case "7d":
		period = 7 * 24 * time.Hour
	case "30d":
		period = 30 * 24 * time.Hour
	default:
		return false
	}
	return !w.ResetsAt.IsZero() && w.ResetsAt.Add(-period).After(t)
}

// backdate writes, with the server stopped, the two states nobody can reach
// from the app's pages, and starts the server again.
func (s *seeder) backdate(ctx context.Context) error {
	if err := s.inst.Stop(); err != nil {
		return err
	}
	now := time.Now()
	for _, p := range Personas {
		if p.ExpiredKey {
			k, err := s.key(ctx, p.Sub)
			if err != nil {
				return err
			}
			if err := s.store.UpdateAPIKeyExpiry(ctx, k.ID, now.AddDate(0, 0, -3)); err != nil {
				return err
			}
		}
		if p.UntilDays < 0 {
			u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
			if err != nil {
				return err
			}
			until := now.AddDate(0, 0, p.UntilDays)
			if err := s.store.SetUserProfileUntil(ctx, u.ID, u.ProfileID, &until,
				u.ProfileAfterExpiry, u.RevokeKeyAtExpiry); err != nil {
				return err
			}
		}
	}
	if err := s.inst.Start(ctx); err != nil {
		return err
	}
	// The expiry job runs right after startup, alongside the server.
	for _, p := range Personas {
		if p.UntilDays >= 0 {
			continue
		}
		if err := poll(ctx, 15*time.Second, func() (bool, error) {
			u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
			return err == nil && u.ProfileExpiresAt == nil, err
		}); err != nil {
			return fmt.Errorf("%s: the expiry job did not apply the deadline: %w", p.Sub, err)
		}
	}
	return nil
}

// markMissing lets the app find out that the gateway lost a key, which it does
// when the owner's dashboard asks the gateway for its usage.
func (s *seeder) markMissing(ctx context.Context) error {
	for _, p := range Personas {
		if !p.MissingKey {
			continue
		}
		if _, err := s.clients[p.Sub].Page(ctx, "/"); err != nil {
			return fmt.Errorf("%s: %w", p.Sub, err)
		}
		k, err := s.key(ctx, p.Sub)
		if err != nil {
			return err
		}
		if k.MissingSince == nil {
			return fmt.Errorf("%s: the dashboard did not mark the key missing", p.Sub)
		}
	}
	return nil
}

// WaitOutTheHour waits for the next hour when it is only minutes away. Quota
// windows reset on the clock, the hourly ones on the hour and the daily ones
// at midnight UTC, and one that reset during the seed would show none of the
// usage it just made. `make seed` waits, so that every bar has something to
// show; the browser tests do not, since they check no usage in an hourly
// window and Seed copes with a window that reset.
func WaitOutTheHour(ctx context.Context, out io.Writer) error {
	now := time.Now()
	next := now.Truncate(time.Hour).Add(time.Hour)
	wait := next.Sub(now)
	if wait >= 3*time.Minute {
		return nil
	}
	wait += 15 * time.Second
	fmt.Fprintf(out, "Waiting %v for the quota windows to reset on the hour first\n", wait.Round(time.Second))
	return sleep(ctx, wait)
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func poll(ctx context.Context, timeout time.Duration, done func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := done()
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = errors.New("timed out")
			}
			return err
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

func readMockUsers(path string) (map[string]mockUser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []mockUser
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	users := map[string]mockUser{}
	for _, u := range list {
		users[u.Sub] = u
	}
	return users, nil
}

// Write saves the manifest as JSON.
func (m *Manifest) Write(path string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadManifest loads a manifest that Write saved.
func ReadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Cleanup deletes what a seeded instance created in LiteLLM: the keys its
// database still knows, and its users, which hold the spend and any key the
// database no longer knows. The instance's own files stay.
func Cleanup(ctx context.Context, dbPath, prefix string) error {
	if prefix == "" {
		return errors.New("cleanup needs the run's prefix, or it would name users of no run")
	}
	gw := litellm.NewClient(e2etest.BaseURL(), e2etest.MasterKey())
	var errs []error

	db, err := database.Open(dbPath)
	if err == nil {
		store := database.NewStore(db)
		keys, err := store.ListAPIKeys(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		for _, k := range keys {
			if err := gw.DeleteKey(ctx, k.KeyRef); err != nil {
				errs = append(errs, err)
			}
		}
		store.Close()
	} else {
		errs = append(errs, err)
	}

	// One user at a time: only personas who generated a key have a user in
	// the gateway, and deleting one that does not exist is fine.
	for _, p := range Personas {
		if err := e2etest.DeleteUser(ctx, prefix+":"+p.Sub); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
