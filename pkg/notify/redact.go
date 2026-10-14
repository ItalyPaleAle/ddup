package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

const redacted = "[redacted]"

// Secrets that are not known in advance, as found in error messages
var (
	bearerRegexp      = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	queryRegexp       = regexp.MustCompile(`(?i)([?&](?:key|api_?key|token|access_token|secret|password|sig|signature)=)[^&\s"]+`)
	secretFieldRegexp = regexp.MustCompile(`(?i)token|secret|key|password`)
)

// redactor removes secrets from text
type redactor struct {
	// Secrets to remove, longest first so that a secret that contains another is removed entirely
	secrets []string
}

// newRedactor returns a redactor that removes the given secrets, and the patterns that look like secrets
func newRedactor(secrets ...string) *redactor {
	list := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			list = append(list, s)
		}
	}
	slices.SortFunc(list, func(a, b string) int {
		return len(b) - len(a)
	})
	return &redactor{
		secrets: slices.Compact(list),
	}
}

// String removes the secrets from s
func (r *redactor) String(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	s = bearerRegexp.ReplaceAllString(s, "${1}"+redacted)
	s = queryRegexp.ReplaceAllString(s, "${1}"+redacted)
	return s
}

// Error returns an error with the message of err without the secrets
// The returned error wraps err for errors.Is and errors.As, but it is the only text that must be logged
func (r *redactor) Error(err error) error {
	if err == nil {
		return nil
	}
	return &redactedError{
		msg: r.String(err.Error()),
		err: err,
	}
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string {
	return e.msg
}

func (e *redactedError) Unwrap() error {
	return e.err
}

// urlSecrets returns the parts of a URL that can be secrets: all of it, the path (like the token in Slack and Discord webhooks and the topic in ntfy), the query string, and the password
func urlSecrets(raw string) []string {
	secrets := []string{raw}
	u, err := url.Parse(raw)
	if err != nil {
		return secrets
	}

	path := strings.Trim(u.Path, "/")
	if path != "" {
		secrets = append(secrets, path, strings.Trim(u.EscapedPath(), "/"))
	}
	if u.RawQuery != "" {
		secrets = append(secrets, u.RawQuery)
	}
	if u.User != nil {
		secrets = append(secrets, u.User.Username())
		password, ok := u.User.Password()
		if ok {
			secrets = append(secrets, password)
		}
	}
	return secrets
}

// connStringSecrets returns the parts of a connection string that can be secrets
// The host is only included for the providers that use it for the API key
func connStringSecrets(raw string) []string {
	secrets := []string{raw}
	u, err := url.Parse(raw)
	if err != nil {
		return secrets
	}

	switch u.Scheme {
	case "resend", "sendgrid":
		secrets = append(secrets, u.Host)
	}
	if u.User != nil {
		secrets = append(secrets, u.User.Username())
		password, ok := u.User.Password()
		if ok {
			secrets = append(secrets, password)
		}
	}
	for _, values := range u.Query() {
		secrets = append(secrets, values...)
	}
	return secrets
}

// providerSecrets returns the values of the settings of a DNS provider that look like secrets (tokens, keys, and similar)
func providerSecrets(provider any) []string {
	val := reflect.ValueOf(provider)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil
	}

	var secrets []string
	for i := range val.NumField() {
		field := val.Field(i)
		switch {
		case field.Kind() == reflect.Pointer && !field.IsNil() && field.Elem().Kind() == reflect.Struct:
			secrets = append(secrets, providerSecrets(field.Interface())...)
		case field.Kind() == reflect.String && secretFieldRegexp.MatchString(val.Type().Field(i).Name):
			secrets = append(secrets, field.String())
		}
	}
	return secrets
}

// redactingHandler is a slog.Handler that removes secrets from the messages and attributes of the records
// It's used for the loggers passed to the libraries, which include URLs in their errors
type redactingHandler struct {
	inner slog.Handler
	r     *redactor
}

func newRedactingLogger(inner *slog.Logger, r *redactor) *slog.Logger {
	return slog.New(&redactingHandler{
		inner: inner.Handler(),
		r:     r,
	})
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	clean := slog.NewRecord(rec.Time, rec.Level, h.r.String(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(h.attr(a))
		return true
	})
	err := h.inner.Handle(ctx, clean)
	if err != nil {
		return fmt.Errorf("inner log handler failed: %w", err)
	}
	return nil
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attr(a)
	}
	return &redactingHandler{
		inner: h.inner.WithAttrs(clean),
		r:     h.r,
	}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{
		inner: h.inner.WithGroup(name),
		r:     h.r,
	}
}

func (h *redactingHandler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()

	// Strings, groups, and any other value that is logged as text can include secrets, while numbers and similar values cannot
	switch {
	case v.Kind() == slog.KindString:
		return slog.String(a.Key, h.r.String(v.String()))
	case v.Kind() == slog.KindGroup:
		group := v.Group()
		clean := make([]any, len(group))
		for i, ga := range group {
			clean[i] = h.attr(ga)
		}
		return slog.Group(a.Key, clean...)
	case v.Kind() == slog.KindAny:
		// Errors and other values are logged as text, so they can be cleaned
		return slog.String(a.Key, h.r.String(fmt.Sprint(v.Any())))
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}
