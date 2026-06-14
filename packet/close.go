package packet

import "errors"

// ErrServerClosed is the sentinel returned by a connect/login when the server
// sent a CTRL_CLOSE control packet — a rejection (wrong password/version, server
// full, ban). errors.Is(err, ErrServerClosed) detects it across both protocols,
// so the login fails fast with the reason instead of looping until the context
// deadline (V109, B10).
var ErrServerClosed = errors.New("packet: server sent CTRL_CLOSE")

// ServerClosedError carries the human-readable reason text from a server
// CTRL_CLOSE. It unwraps to ErrServerClosed; the client layer classifies the
// reason into a DisconnectReason (V34). Reason is "" when the server sent no text.
type ServerClosedError struct{ Reason string }

// Error implements error.
func (e *ServerClosedError) Error() string {
	if e.Reason == "" {
		return ErrServerClosed.Error()
	}
	return ErrServerClosed.Error() + ": " + e.Reason
}

// Unwrap reports ErrServerClosed so errors.Is(err, ErrServerClosed) holds.
func (e *ServerClosedError) Unwrap() error { return ErrServerClosed }
