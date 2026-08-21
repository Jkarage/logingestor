package clienterrorbus

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

// Errors returned when uploading a source map.
var (
	ErrArtifactTooLarge = fmt.Errorf("a source map may be at most %d bytes", MaxArtifactBytes)
	ErrArtifactEmpty    = errors.New("source map is empty")
	ErrArtifactRelease  = errors.New("release is required")
	ErrArtifactInvalid  = errors.New("file is not a valid source map")
)

// NewArtifact is one source map being uploaded.
type NewArtifact struct {
	Release string

	// FileName is the generated file the map describes, as it appears in a stack
	// trace. A ".map" suffix is stripped, because that is what CI has on disk and
	// "index-64s.js" is what the browser reports.
	FileName string

	Content    []byte
	UploadedBy string
}

// UploadArtifact stores one source map for a release and queues that release's
// existing events to be grouped again.
//
// Two things happen to the file on the way in. Its embedded sourcesContent is
// dropped, and it is gzipped. Dropping the sources is the important one: it is
// the bulk of the file and we never read it, so keeping it would mean holding a
// copy of the frontend source at rest for no purpose. What remains is the
// mapping table, which is what resolution actually needs.
func (b *Business) UploadArtifact(ctx context.Context, na NewArtifact) (Artifact, error) {
	release := strings.TrimSpace(na.Release)
	switch {
	case release == "":
		return Artifact{}, ErrArtifactRelease
	case len(na.Content) == 0:
		return Artifact{}, ErrArtifactEmpty
	case len(na.Content) > MaxArtifactBytes:
		return Artifact{}, ErrArtifactTooLarge
	}

	stripped, err := stripSourcesContent(na.Content)
	if err != nil {
		return Artifact{}, err
	}

	compressed, err := gzipBytes(stripped)
	if err != nil {
		return Artifact{}, fmt.Errorf("compress: %w", err)
	}

	a := Artifact{
		ID:         uuid.New(),
		Release:    truncate(release, MaxReleaseLen),
		FileName:   generatedFileName(na.FileName),
		ByteSize:   len(compressed),
		UploadedBy: truncate(strings.TrimSpace(na.UploadedBy), 120),
	}

	if a.FileName == "" {
		return Artifact{}, ErrArtifactInvalid
	}

	if err := b.storer.UpsertArtifact(ctx, a, compressed); err != nil {
		return Artifact{}, fmt.Errorf("upsertartifact: %w", err)
	}

	// Drop the parsed cache, which is holding a remembered miss for this file
	// from every event that arrived before the upload.
	b.maps.forget()

	// Events already grouped from minified frames are queued to be grouped
	// again. CI usually uploads before the first crash, but not always — a map
	// arriving late should fix the issues that were already filed, not only the
	// ones that come next.
	queued, err := b.storer.MarkReleaseForRegroup(ctx, a.Release, ResolvedFingerprintVersion)
	if err != nil {
		return Artifact{}, fmt.Errorf("markreleaseforregroup: %w", err)
	}

	b.log.Info(ctx, "clienterror: source map stored",
		"release", a.Release, "file", a.FileName, "bytes", a.ByteSize, "requeued", queued)

	return a, nil
}

// QueryArtifacts lists what has been uploaded for a release, so a deploy can be
// checked without a crash to test it on.
func (b *Business) QueryArtifacts(ctx context.Context, release string) ([]Artifact, error) {
	out, err := b.storer.QueryArtifactsByRelease(ctx, strings.TrimSpace(release))
	if err != nil {
		return nil, fmt.Errorf("queryartifactsbyrelease: %w", err)
	}

	return out, nil
}

// generatedFileName reduces an uploaded path to the file a stack trace names:
// "dist/assets/index-64s.js.map" becomes "index-64s.js".
func generatedFileName(name string) string {
	name = strings.TrimSpace(name)
	name = name[strings.LastIndexByte(name, '/')+1:]
	name = strings.TrimSuffix(name, ".map")

	return truncate(name, 200)
}

// stripSourcesContent removes the embedded original sources from a map, and in
// doing so proves the upload is a source map at all.
func stripSourcesContent(content []byte) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrArtifactInvalid, err)
	}

	// version and mappings are the two fields resolution cannot work without.
	if _, ok := doc["mappings"]; !ok {
		return nil, fmt.Errorf("%w: no mappings", ErrArtifactInvalid)
	}
	if _, ok := doc["version"]; !ok {
		return nil, fmt.Errorf("%w: no version", ErrArtifactInvalid)
	}

	delete(doc, "sourcesContent")

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrArtifactInvalid, err)
	}

	return out, nil
}

// gzipBytes compresses a map for storage.
func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// gunzipBytes decompresses a stored map.
func gunzipBytes(in []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	// The decompressed size is bounded by what was accepted on upload.
	return io.ReadAll(io.LimitReader(zr, MaxArtifactBytes))
}
