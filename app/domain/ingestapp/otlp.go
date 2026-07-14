package ingestapp

import (
	"context"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/app/sdk/mid"
	"github.com/jkarage/logingestor/business/domain/logbus"
	"github.com/jkarage/logingestor/business/sdk/ingest"
	"github.com/jkarage/logingestor/foundation/web"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// otlpResourceAttrMap maps OTel semantic-convention resource/log attribute keys
// onto infra columns. Anything not listed stays in the attributes blob.
var otlpResourceAttrMap = map[string]func(*logbus.Infra, string){
	"host.name":          func(in *logbus.Infra, v string) { in.Host = v },
	"k8s.pod.name":       func(in *logbus.Infra, v string) { in.Pod = v },
	"k8s.namespace.name": func(in *logbus.Infra, v string) { in.Namespace = v },
	"k8s.cluster.name":   func(in *logbus.Infra, v string) { in.Cluster = v },
	"k8s.container.name": func(in *logbus.Infra, v string) { in.Container = v },
	"container.name":     func(in *logbus.Infra, v string) { in.Container = v },
	"service.name":       func(in *logbus.Infra, v string) { in.Unit = v },
	"cloud.region":       func(in *logbus.Infra, v string) { in.Region = v },
	"cloud.resource.id":  func(in *logbus.Infra, v string) { in.CloudResourceID = v },
}

// otlp handles POST /v1/ingest/otlp — OTLP/HTTP logs in protobuf or JSON.
func (a *app) otlp(ctx context.Context, r *http.Request) web.Encoder {
	src, err := mid.GetSource(ctx)
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	body, err := readLimited(r.Body, maxBodyBytes)
	if err != nil {
		return errs.New(errs.InvalidArgument, fmt.Errorf("read body: %w", err))
	}

	var data logspb.LogsData
	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "json"):
		if err := protojson.Unmarshal(body, &data); err != nil {
			return errs.New(errs.InvalidArgument, fmt.Errorf("otlp json: %w", err))
		}
	default: // application/x-protobuf (and unspecified)
		if err := proto.Unmarshal(body, &data); err != nil {
			return errs.New(errs.InvalidArgument, fmt.Errorf("otlp protobuf: %w", err))
		}
	}

	now := time.Now().UTC()
	recs := otlpToRecords(&data, src.Kind, now)
	if len(recs) == 0 {
		return errs.New(errs.InvalidArgument, fmt.Errorf("no log records in OTLP payload"))
	}
	if len(recs) > maxRecords {
		return errs.Errorf(errs.InvalidArgument, "too many records: %d (max %d)", len(recs), maxRecords)
	}

	return a.process(ctx, src, recs, nil, len(body))
}

// otlpToRecords flattens an OTLP LogsData into normalized ingest records,
// folding resource attributes into each record and mapping semantic-convention
// keys onto infra columns.
func otlpToRecords(data *logspb.LogsData, defaultSource string, now time.Time) []ingest.Record {
	var recs []ingest.Record

	for _, rl := range data.GetResourceLogs() {
		resInfra := logbus.Infra{}
		resAttrs := map[string]any{}
		if res := rl.GetResource(); res != nil {
			collectAttrs(res.GetAttributes(), &resInfra, resAttrs)
		}

		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				recs = append(recs, otlpRecord(lr, resInfra, resAttrs, defaultSource, now))
			}
		}
	}

	return recs
}

func otlpRecord(lr *logspb.LogRecord, resInfra logbus.Infra, resAttrs map[string]any, defaultSource string, now time.Time) ingest.Record {
	// Start attributes from the resource, then overlay record attributes.
	attrs := make(map[string]any, len(resAttrs)+4)
	maps.Copy(attrs, resAttrs)

	infra := resInfra
	collectAttrs(lr.GetAttributes(), &infra, attrs)

	// Preserve the original severity number; map to a canonical level.
	sevNum := int(lr.GetSeverityNumber())
	attrs[ingest.SeverityAttrKey] = sevNum
	if st := lr.GetSeverityText(); st != "" {
		attrs["severity_text"] = st
	}

	// Trace correlation into the existing meta keys.
	if tid := lr.GetTraceId(); len(tid) > 0 {
		attrs["trace_id"] = hex.EncodeToString(tid)
	}
	if sid := lr.GetSpanId(); len(sid) > 0 {
		attrs["span_id"] = hex.EncodeToString(sid)
	}

	ts := now
	if t := lr.GetTimeUnixNano(); t > 0 {
		ts = time.Unix(0, int64(t)).UTC()
	} else if t := lr.GetObservedTimeUnixNano(); t > 0 {
		ts = time.Unix(0, int64(t)).UTC()
	}

	return ingest.Record{
		Level:      ingest.OTelSeverityToLevel(sevNum),
		Message:    anyValueToString(lr.GetBody()),
		Source:     defaultSource,
		Timestamp:  ts,
		Infra:      infra,
		Attributes: attrs,
	}
}

// collectAttrs maps known semantic-convention keys onto infra and puts the rest
// into the attrs map.
func collectAttrs(kvs []*commonpb.KeyValue, infra *logbus.Infra, attrs map[string]any) {
	for _, kv := range kvs {
		key := kv.GetKey()
		val := anyValueToGo(kv.GetValue())
		if setter, ok := otlpResourceAttrMap[key]; ok {
			if s, isStr := val.(string); isStr {
				setter(infra, s)
				continue
			}
		}
		attrs[key] = val
	}
}

// anyValueToString renders an OTLP body for the message field.
func anyValueToString(v *commonpb.AnyValue) string {
	switch val := anyValueToGo(v).(type) {
	case string:
		return val
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

// anyValueToGo converts an OTLP AnyValue into a plain Go value.
func anyValueToGo(v *commonpb.AnyValue) any {
	if v == nil {
		return nil
	}
	switch v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.GetStringValue()
	case *commonpb.AnyValue_BoolValue:
		return v.GetBoolValue()
	case *commonpb.AnyValue_IntValue:
		return v.GetIntValue()
	case *commonpb.AnyValue_DoubleValue:
		return v.GetDoubleValue()
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(v.GetBytesValue())
	case *commonpb.AnyValue_ArrayValue:
		arr := v.GetArrayValue().GetValues()
		out := make([]any, len(arr))
		for i, e := range arr {
			out[i] = anyValueToGo(e)
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		m := map[string]any{}
		for _, kv := range v.GetKvlistValue().GetValues() {
			m[kv.GetKey()] = anyValueToGo(kv.GetValue())
		}
		return m
	default:
		return nil
	}
}
