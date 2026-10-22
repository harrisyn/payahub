package connector

import (
	"errors"
	"fmt"
)

// ErrorCode is the one error taxonomy for every payer. core-api-v2 recognised
// payer failures by matching free text in each adapter; here a connector
// translates its payer's message into a code once, at the edge, and
// everything above it (engine, API, dashboard) works with the code.
type ErrorCode string

const (
	MemberNotFound        ErrorCode = "member_not_found"
	MemberInactive        ErrorCode = "member_inactive"
	OTPRequired           ErrorCode = "otp_required"
	AuthorizationRequired ErrorCode = "authorization_required"
	ClaimExpired          ErrorCode = "claim_expired"
	ClaimClosed           ErrorCode = "claim_closed"
	LineAlreadyAdded      ErrorCode = "line_already_added"
	LineNotFound          ErrorCode = "line_not_found"
	CodeNotRecognised     ErrorCode = "code_not_recognised"
	InvalidCredentials    ErrorCode = "invalid_credentials"
	NotSupported          ErrorCode = "not_supported"
	Validation            ErrorCode = "validation"
	Timeout               ErrorCode = "timeout"
	Connection            ErrorCode = "connection"
	PayerRefused          ErrorCode = "payer_refused"
	// AwaitingPayer means the payer must act first (approve a line, say).
	// The claim stays submitted and the queue checks again with backoff.
	AwaitingPayer ErrorCode = "awaiting_payer"
	Unknown               ErrorCode = "unknown"
)

// Error is what every connector returns on failure.
type Error struct {
	Code    ErrorCode
	Message string
	// UserMessage is safe to show a biller; never carries internal identifiers.
	UserMessage string
	// PayerMessage is the payer's own words, kept for the AI classifier and audit.
	PayerMessage string
	// Cause is the transport-level error behind this one, if any.
	Cause error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *Error) Unwrap() error { return e.Cause }

// Retryable errors are transport failures: the payer may or may not have
// received the request, so the worker reconciles before resending.
func (e *Error) Retryable() bool {
	return e.Code == Timeout || e.Code == Connection || e.Code == Unknown || e.Code == AwaitingPayer
}

// Final errors mean the payer decided against this claim; retrying will not help.
func (e *Error) Final() bool {
	switch e.Code {
	case PayerRefused, MemberNotFound, MemberInactive, ClaimClosed, ClaimExpired, CodeNotRecognised, Validation:
		return true
	}
	return false
}

func Errorf(code ErrorCode, format string, args ...any) *Error {
	msg := fmt.Sprintf(format, args...)
	return &Error{Code: code, Message: msg, UserMessage: msg}
}

// AsError returns err as a connector Error, wrapping anything else as Unknown.
func AsError(err error) *Error {
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	return &Error{Code: Unknown, Message: err.Error(), UserMessage: "The payer could not be reached."}
}
