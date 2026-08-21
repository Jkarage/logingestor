package clienterrorbus

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sourcemap/sourcemap"
	"github.com/google/uuid"
)

// ResolvedFingerprintVersion is stamped on events grouped from de-minified
// frames. Events at a lower version were grouped from minified ones and are
// re-grouped when a map for their release arrives.
const ResolvedFingerprintVersion = 2

// MaxArtifactBytes caps one uploaded map. Bundles get large, but a map an order
// of magnitude past this is a mistake rather than a build.
const MaxArtifactBytes = 32 * 1024 * 1024

// Artifact is one uploaded source map.
type Artifact struct {
	ID          uuid.UUID
	Release     string
	FileName    string
	ByteSize    int
	UploadedBy  string
	DateCreated time.Time
}

// SourceMapLookup returns the map for one generated file of a release, already
// decompressed, or ok=false when there is none.
//
// It is an interface so resolution can be tested without a database, and so the
// worker can cache: parsing a multi-megabyte map for every frame of every event
// would cost more than the grouping it feeds.
type SourceMapLookup interface {
	MapFor(ctx context.Context, release, fileName string) (*sourcemap.Consumer, bool)
}

// ResolveStack rewrites a minified stack using the maps for its release.
//
// Frames that cannot be resolved are kept exactly as they were: a partially
// resolved stack is far more useful than none, and it is normal for one frame to
// come from a file we have no map for. resolved reports whether anything at all
// was rewritten, which is what decides the fingerprint version.
//
// One case does not resolve and cannot be made to: a column past the last
// mapping on its generated line. The lookup finds the nearest mapping at or
// before a column, and there is nothing before the end of the table to fall back
// to. In a real bundle line 1 carries thousands of mappings, so this only
// affects a frame in the final expression of the file.
func ResolveStack(ctx context.Context, stack, release string, maps SourceMapLookup) (out string, resolved bool) {
	if stack == "" || release == "" || maps == nil {
		return stack, false
	}

	lines := strings.Split(stack, "\n")

	for i, line := range lines {
		frame, ok := parseFrameLine(line)
		if !ok {
			continue
		}

		consumer, ok := maps.MapFor(ctx, release, path.Base(frame.File))
		if !ok {
			continue
		}

		genLine, err := strconv.Atoi(frame.Line)
		if err != nil {
			continue
		}
		genCol, err := strconv.Atoi(frame.Column)
		if err != nil {
			continue
		}

		source, name, srcLine, srcCol, ok := consumer.Source(genLine, genCol)
		if !ok || source == "" {
			continue
		}

		if name == "" {
			// A map without a name for this position still gives us the file and
			// line, which is the part that matters. Keep whatever the browser
			// called the function.
			name = frame.Function
		}

		lines[i] = renderFrame(name, source, srcLine, srcCol)
		resolved = true
	}

	if !resolved {
		return stack, false
	}

	return strings.Join(lines, "\n"), true
}

// renderFrame writes a resolved frame in the shape ParseStack reads back, so a
// resolved stack fingerprints through exactly the same code path as a raw one.
func renderFrame(function, file string, line, column int) string {
	if function == "" {
		return fmt.Sprintf("    at %s:%d:%d", file, line, column)
	}

	return fmt.Sprintf("    at %s (%s:%d:%d)", function, file, line, column)
}

// parseFrameLine extracts one frame from a stack line, reusing the parser the
// fingerprint uses so the two cannot disagree about what a frame is.
func parseFrameLine(line string) (Frame, bool) {
	frames := ParseStack(line)
	if len(frames) != 1 {
		return Frame{}, false
	}

	f := frames[0]
	if f.File == "" || f.Line == "" || f.Column == "" {
		return Frame{}, false
	}

	return f, true
}

// sourceMapCache parses stored maps on demand and keeps a few around.
//
// A map is megabytes of JSON that parses into a large mapping table, and one
// event's stack asks for the same file on every frame. Without a cache the
// worker would parse the whole bundle's map per frame; with one it parses it
// once per release and then answers from memory.
type sourceMapCache struct {
	mu      sync.Mutex
	entries map[string]*sourcemap.Consumer
	misses  map[string]struct{}
	fetch   func(ctx context.Context, release, fileName string) ([]byte, error)
}

// maxCachedMaps bounds the cache by count rather than bytes, because a parsed
// mapping table's footprint is not something we can measure cheaply. Two or
// three releases are in flight at once in practice; eight is slack.
const maxCachedMaps = 8

func newSourceMapCache(fetch func(ctx context.Context, release, fileName string) ([]byte, error)) *sourceMapCache {
	return &sourceMapCache{
		entries: make(map[string]*sourcemap.Consumer),
		misses:  make(map[string]struct{}),
		fetch:   fetch,
	}
}

// MapFor implements SourceMapLookup.
func (c *sourceMapCache) MapFor(ctx context.Context, release, fileName string) (*sourcemap.Consumer, bool) {
	key := release + "|" + fileName

	c.mu.Lock()
	if consumer, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return consumer, true
	}
	if _, missed := c.misses[key]; missed {
		// A file we have already looked for and not found. Most frames in a stack
		// are vendor chunks with no map, and re-querying for each one turns every
		// event into a handful of pointless round trips.
		c.mu.Unlock()
		return nil, false
	}
	c.mu.Unlock()

	raw, err := c.fetch(ctx, release, fileName)
	if err != nil || len(raw) == 0 {
		c.remember(key, nil)
		return nil, false
	}

	plain, err := gunzipBytes(raw)
	if err != nil {
		c.remember(key, nil)
		return nil, false
	}

	consumer, err := sourcemap.Parse(fileName+".map", plain)
	if err != nil {
		c.remember(key, nil)
		return nil, false
	}

	c.remember(key, consumer)

	return consumer, true
}

// remember stores a hit or a miss, clearing the cache when it grows past its
// bound. Clearing wholesale rather than evicting one entry is fine here: the
// working set is one release, so a clear is followed by one re-parse.
func (c *sourceMapCache) remember(key string, consumer *sourcemap.Consumer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= maxCachedMaps {
		c.entries = make(map[string]*sourcemap.Consumer)
		c.misses = make(map[string]struct{})
	}

	if consumer == nil {
		c.misses[key] = struct{}{}
		return
	}

	c.entries[key] = consumer
}

// forget drops everything, so a newly uploaded map is picked up rather than
// shadowed by a remembered miss.
func (c *sourceMapCache) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*sourcemap.Consumer)
	c.misses = make(map[string]struct{})
}
