package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"unicode"

	tsapi "tailscale.com/client/tailscale/v2"
)

type shareError struct {
	code      string
	cause     error
	apiStatus int
	detail    string
}

func (e *shareError) Error() string { return e.code }
func (e *shareError) Unwrap() error { return e.cause }
func shareErr(code string) error    { return &shareError{code: code} }
func shareCode(err error) string {
	var failure *shareError
	if errors.As(err, &failure) {
		return failure.code
	}
	return "setup_failed"
}
func shareHTTPError(err error) (int, string, string) {
	code := shareCode(err)
	status := http.StatusServiceUnavailable
	switch code {
	case "authorization":
		status = 403
	case "revision":
		status = 409
	case "invalid_request", "ineligible":
		status = 400
	}
	message := shareErrorMessage(code)
	var failure *shareError
	if code == "ineligible" && errors.As(err, &failure) {
		message = failure.detail
	}
	return status, code, message
}
func shareErrorMessage(code string) string {
	switch code {
	case "authorization":
		return sharePermissionMessage
	case "revision":
		return shareRevisionMessage
	case "invalid_request":
		return "Invalid request."
	case "unavailable":
		return "Share unavailable."
	case "capacity":
		return "Sharing is at this instance's limit. Stop another share, then retry."
	case "storage", "state_write_failed":
		return shareWriteMessage
	case "stop_storage":
		return shareStopStorageMessage
	case "oauth_missing":
		return shareOAuthMessage
	case "scope_denied":
		return "tailhome cannot create the public node. Allow Auth Keys write for tag:tailhome-share on its OAuth client, then retry."
	case "key_permission_denied":
		return "tailhome cannot create the public node. Make tag:tailhome an owner of tag:tailhome-share in tagOwners, or give the OAuth client tag:tailhome-share, then retry."
	case "key_request_invalid":
		return "The public node auth-key request was rejected. Ask the operator to check the HTTP status in tailhome's logs, then retry."
	case "key_rate_limited":
		return "Public node enrollment was rate limited. Wait before retrying. Ask the operator to check tailhome's logs."
	case "key_request_failed":
		return "The public node auth-key request failed. Ask the operator to check the HTTP status in tailhome's logs, then retry."
	case "tailnet_lock":
		return "Public sharing is not supported on tailnets with Tailnet Lock."
	case "funnel_denied":
		return "Funnel is not allowed for the public node. Ask a tailnet admin to allow tag:tailhome-share, then retry."
	case "dns_missing":
		return "Enable MagicDNS and HTTPS certificates in tailnet DNS settings before sharing."
	case "certificate_rate_limited":
		return "Certificate issuance was rate limited. Wait before retrying. Creating more shares or deleting nodes will not reset the limit. Ask the operator to check the issuer's retry information."
	}
	return "Could not start sharing. Check setup and try again."
}
func shareEnrollmentFailure(err error) error {
	var failure *shareError
	if errors.As(err, &failure) {
		return failure
	}
	if cannotMintKeys(err) {
		return &shareError{code: "scope_denied", apiStatus: 403}
	}
	var api tsapi.APIError
	if value, ok := errors.AsType[tsapi.APIError](err); ok {
		api = value
	} else if pointer, ok := errors.AsType[*tsapi.APIError](err); ok {
		api = *pointer
	} else {
		return shareErr("setup_failed")
	}
	code := "key_request_failed"
	switch api.Status {
	case 401, 403:
		code = "key_permission_denied"
	case 400, 422:
		code = "key_request_invalid"
	case 429:
		code = "key_rate_limited"
	}
	return &shareError{code: code, apiStatus: api.Status}
}

// Only storage errors carry raw causes. API/issuer bodies and keys are discarded.
func shareLog(id, reason string, err error) {
	if err == nil {
		log.Printf("share %s: %s", id, reason)
		return
	}
	var failure *shareError
	if errors.As(err, &failure) && failure.apiStatus != 0 {
		log.Printf("share %s: %s code=%s http_status=%d", id, reason, failure.code, failure.apiStatus)
		return
	}
	if code := shareCode(err); code == "storage" || code == "stop_storage" || code == "state_write_failed" || code == "cleanup_pending" {
		cause := err
		var pathError *os.PathError
		if errors.As(err, &pathError) {
			cause = pathError.Err
		} else if failure != nil && failure.cause != nil {
			cause = failure.cause
		}
		message := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, cause.Error())
		if len(message) > 200 {
			message = message[:200]
		}
		log.Printf("share %s: %s err=%q", id, reason, message)
		return
	}
	log.Printf("share %s: %s code=%s", id, reason, shareCode(err))
}
