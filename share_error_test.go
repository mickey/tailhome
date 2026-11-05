package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"testing"

	tsapi "tailscale.com/client/tailscale/v2"
)

func TestShareKeyAPIErrorMapping(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{{401, "key_permission_denied"}, {403, "key_permission_denied"}, {400, "key_request_invalid"}, {422, "key_request_invalid"}, {429, "key_rate_limited"}, {404, "key_request_failed"}, {409, "key_request_failed"}} {
		api := tsapi.APIError{Status: test.status, Message: "private API body"}
		for _, original := range []error{api, &api, fmt.Errorf("wrapped: %w", api)} {
			failure := shareEnrollmentFailure(original)
			status, code, _ := shareHTTPError(failure)
			if status != 503 || code != test.code {
				t.Fatalf("API status%d => %d %s", test.status, status, code)
			}
		}
	}
}
func TestShareLogsExposeStatusAndStorageCauseNotSecrets(t *testing.T) {
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	secret := "tskey-api-private"
	shareLog("public-id", "failure", shareEnrollmentFailure(tsapi.APIError{Status: 403, Message: "https://user:password@example.invalid/?token=" + secret}))
	shareLog("public-id", "failure", errors.New(secret))
	shareLog("public-id", "failure", &shareError{code: "storage", cause: &os.PathError{Op: "write", Path: "/private/" + secret, Err: syscall.ENOSPC}})
	shareLog("public-id", "cleanup", &shareError{code: "cleanup_pending", cause: &os.PathError{Op: "remove", Path: "/private/" + secret, Err: syscall.EACCES}})
	line := output.String()
	if strings.Contains(line, secret) || strings.Contains(line, "password") || strings.Contains(line, "example.invalid") {
		t.Fatal("log exposed credential material")
	}
	if !strings.Contains(line, "403") || !strings.Contains(line, syscall.ENOSPC.Error()) || !strings.Contains(line, syscall.EACCES.Error()) {
		t.Fatal("log hid actionable status or storage error")
	}
}
