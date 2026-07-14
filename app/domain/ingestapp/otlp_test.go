package ingestapp_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/jkarage/logingestor/business/domain/logbus"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func sampleLogsData() *logspb.LogsData {
	strVal := func(s string) *commonpb.AnyValue {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
	}
	return &logspb.LogsData{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "host.name", Value: strVal("node-7")},
					{Key: "k8s.namespace.name", Value: strVal("prod")},
					{Key: "service.name", Value: strVal("checkout")},
					{Key: "cloud.region", Value: strVal("us-east-1")},
				},
			},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{
					{
						TimeUnixNano:   uint64(1_700_000_000_000_000_000),
						SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, // 17 -> ERROR
						Body:           strVal("payment failed"),
						TraceId:        []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
						Attributes:     []*commonpb.KeyValue{{Key: "order_id", Value: strVal("o-123")}},
					},
					{
						SeverityNumber: logspb.SeverityNumber_SEVERITY_NUMBER_INFO, // 9 -> INFO
						Body:           strVal("health ok"),
					},
				},
			}},
		}},
	}
}

func Test_otlp_Protobuf(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	raw, err := proto.Marshal(sampleLogsData())
	if err != nil {
		t.Fatal(err)
	}

	resp := doPostBytes(t, srv, "/v1/ingest/otlp", "application/x-protobuf", rawKey, raw)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202: %s", resp.StatusCode, b)
	}

	assertOTLPMapping(t, logBus, src.ProjectID.String())
}

func Test_otlp_JSON(t *testing.T) {
	src, rawKey := activeSource()
	logBus := &fakeLogBus{}
	srv := newTestServer(t, fakeSourceBus{src: src}, logBus)

	raw, err := protojson.Marshal(sampleLogsData())
	if err != nil {
		t.Fatal(err)
	}

	resp := doPostBytes(t, srv, "/v1/ingest/otlp", "application/json", rawKey, raw)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 202: %s", resp.StatusCode, b)
	}

	assertOTLPMapping(t, logBus, src.ProjectID.String())
}

func assertOTLPMapping(t *testing.T, logBus *fakeLogBus, wantProject string) {
	t.Helper()
	logBus.mu.Lock()
	defer logBus.mu.Unlock()

	if len(logBus.created) != 2 {
		t.Fatalf("created %d logs, want 2", len(logBus.created))
	}

	var errLog *logbus.NewLog
	for i := range logBus.created {
		if logBus.created[i].Message == "payment failed" {
			errLog = &logBus.created[i]
		}
		if logBus.created[i].ProjectID.String() != wantProject {
			t.Errorf("project = %s, want %s", logBus.created[i].ProjectID, wantProject)
		}
		if logBus.created[i].SourceType != logbus.SourceTypeInfra {
			t.Errorf("source_type = %q, want infra", logBus.created[i].SourceType)
		}
	}

	if errLog == nil {
		t.Fatal("missing 'payment failed' record")
	}
	if !errLog.Level.Equal(logbus.LevelError) {
		t.Errorf("severity 17 mapped to %s, want ERROR", errLog.Level)
	}
	if errLog.Infra.Host != "node-7" || errLog.Infra.Namespace != "prod" || errLog.Infra.Unit != "checkout" || errLog.Infra.Region != "us-east-1" {
		t.Errorf("infra mapping wrong: %+v", errLog.Infra)
	}
	if errLog.Attributes[ingestSeverityKey] == nil {
		t.Errorf("original severity not preserved in attributes: %+v", errLog.Attributes)
	}
	if errLog.Attributes["trace_id"] == nil {
		t.Errorf("trace_id not mapped into attributes")
	}
}

// ingestSeverityKey mirrors ingest.SeverityAttrKey without importing it here.
const ingestSeverityKey = "_severity"
