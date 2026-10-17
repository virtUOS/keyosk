// Package e2etest holds what the end-to-end tests against a real LiteLLM
// gateway, and the seed behind the browser tests, share: where the gateway
// is, owners no earlier run has used, and calls with the master key for what
// the portal's own client does not do.
//
// The gateway is the one in dev/docker-compose.yml unless LITELLM_BASE_URL and
// LITELLM_MASTER_KEY point elsewhere.
package e2etest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// BaseURL is the gateway the tests run against.
func BaseURL() string {
	return envOr("LITELLM_BASE_URL", "http://localhost:4000")
}

// MasterKey is the gateway's master key.
func MasterKey() string {
	return envOr("LITELLM_MASTER_KEY", "sk-local-dev")
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Owner is a gateway user ID no earlier run has used, so reruns against a
// long-lived stack never see each other's keys or spend. The user is deleted
// when the test ends, along with any key it still owns.
func Owner(t *testing.T) string {
	t.Helper()
	id := fmt.Sprintf("zz-e2e-%s-%s",
		strings.ToLower(t.Name()), strconv.FormatInt(time.Now().UnixNano(), 36))
	DeleteUserAtCleanup(t, id)
	return id
}

// DeleteUserAtCleanup deletes the gateway user id when the test ends, for
// users a test creates under an ID of its own making.
//
// Cleanups run last-registered first, so a user registered before its keys
// is deleted after them.
func DeleteUserAtCleanup(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		if err := DeleteUser(context.Background(), id); err != nil {
			t.Log(err)
		}
	})
}

// DeleteUser deletes a gateway user, with the spend and any keys it still
// owns; the portal's client has no call for it, since the portal never
// deletes one. A user that was never created is fine: LiteLLM answers 404.
func DeleteUser(ctx context.Context, id string) error {
	resp, err := request(ctx, http.MethodPost, "/user/delete", map[string]any{"user_ids": []string{id}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete gateway user %s: %d: %.200s", id, resp.StatusCode, b)
	}
	return nil
}

// Call sends a request with the master key and decodes the JSON answer into
// out, unless out is nil. Any status but 2xx fails the test.
func Call(t *testing.T, method, path string, body, out any) {
	t.Helper()
	if err := call(method, path, body, out); err != nil {
		t.Fatal(err)
	}
}

func call(method, path string, body, out any) error {
	resp, err := request(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, b)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// request sends a request with the master key, body as JSON unless nil, and
// returns the response for the caller to judge and close.
func request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, BaseURL()+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+MasterKey())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return resp, nil
}
