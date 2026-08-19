package web

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"net"
	"strings"
)

type ctxKey int

const (
	tracerKey ctxKey = iota + 1
	writerKey
)

func setTracer(ctx context.Context, tracer trace.Tracer) context.Context {
	return context.WithValue(ctx, tracerKey, tracer)
}

func addSpan(ctx context.Context, spanName string, keyValues ...attribute.KeyValue) (context.Context, trace.Span) {
	v, ok := ctx.Value(tracerKey).(trace.Tracer)
	if !ok || v == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	ctx, span := v.Start(ctx, spanName)
	span.SetAttributes(keyValues...)

	return ctx, span
}

func setWriter(ctx context.Context, w http.ResponseWriter) context.Context {
	return context.WithValue(ctx, writerKey, w)
}

// GetWriter returns the underlying writer for the request.
func GetWriter(ctx context.Context) http.ResponseWriter {
	v, ok := ctx.Value(writerKey).(http.ResponseWriter)
	if !ok {
		return nil
	}

	return v
}

// =============================================================================

type ctxClientInfoKey int

const clientInfoKey ctxClientInfoKey = 0

// ClientInfo describes where a request came from. It is captured for every
// handled request so audit records can attribute an action to an address
// without every call site having to thread it through.
type ClientInfo struct {
	IP        string
	UserAgent string
}

func setClientInfo(ctx context.Context, ci ClientInfo) context.Context {
	return context.WithValue(ctx, clientInfoKey, ci)
}

// GetClientInfo returns the client address and user agent for this request. The
// zero value is returned for code paths with no request in scope, such as
// background workers.
func GetClientInfo(ctx context.Context) ClientInfo {
	ci, ok := ctx.Value(clientInfoKey).(ClientInfo)
	if !ok {
		return ClientInfo{}
	}
	return ci
}

// clientInfoFrom derives the client address from the request.
//
// X-Forwarded-For is only consulted for its left-most entry, and only as a
// hint: any client can send the header, so it is trustworthy solely because a
// trusted proxy is expected to rewrite it. RemoteAddr is used when it is absent.
func clientInfoFrom(r *http.Request) ClientInfo {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = host
	}

	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
			ip = first
		}
	} else if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		ip = real
	}

	ua := r.Header.Get("User-Agent")
	if len(ua) > 512 {
		ua = ua[:512]
	}

	return ClientInfo{IP: ip, UserAgent: ua}
}
