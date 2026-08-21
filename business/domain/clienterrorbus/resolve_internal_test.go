package clienterrorbus

import (
	"context"
	"strings"
	"testing"

	"github.com/go-sourcemap/sourcemap"
)

// stubMaps serves one map for one file.
type stubMaps struct {
	release  string
	fileName string
	consumer *sourcemap.Consumer
}

func (s stubMaps) MapFor(_ context.Context, release, fileName string) (*sourcemap.Consumer, bool) {
	if release != s.release || fileName != s.fileName {
		return nil, false
	}

	return s.consumer, true
}

// buildMap hand-writes a v3 source map so the test does not need a bundler.
//
// The mappings field is VLQ base64, all values delta-encoded against the
// previous segment. This one describes generated line 1 with two segments:
//
//	column 0  -> src/views/LogsView.jsx line 142 col 8,  name handleFilterChange
//	column 20 -> src/components/LevelPicker.jsx line 32 col 14, name onChange
func buildMap(t *testing.T) *sourcemap.Consumer {
	t.Helper()

	// Segment fields are [genCol, sourceIdx, srcLine, srcCol, nameIdx], each a
	// delta. Line and column are zero-based in the map and one-based in a stack
	// trace, which is why 142 appears as 141.
	//
	//   segment 1: 0, 0, 141, 8, 0    -> "AAqTQA" style values, encoded below
	//   segment 2: +20, +1, -110, +6, +1
	raw := `{
		"version": 3,
		"file": "index-64s.js",
		"sources": ["src/views/LogsView.jsx", "src/components/LevelPicker.jsx"],
		"names": ["handleFilterChange", "onChange"],
		"mappings": "` + vlqSegments() + `"
	}`

	consumer, err := sourcemap.Parse("index-64s.js.map", []byte(raw))
	if err != nil {
		t.Fatalf("the hand-written map does not parse: %v", err)
	}

	return consumer
}

// vlqSegments encodes the two segments described above.
func vlqSegments() string {
	first := encodeVLQ(0) + encodeVLQ(0) + encodeVLQ(141) + encodeVLQ(8) + encodeVLQ(0)
	second := encodeVLQ(20) + encodeVLQ(1) + encodeVLQ(31-141) + encodeVLQ(14-8) + encodeVLQ(1)

	return first + "," + second
}

// encodeVLQ writes one base64 VLQ value, the encoding the mappings field uses:
// the sign is the low bit, then five-bit groups, most significant last, with the
// continuation bit set on all but the final group.
func encodeVLQ(value int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	v := value << 1
	if value < 0 {
		v = (-value << 1) | 1
	}

	var out strings.Builder
	for {
		digit := v & 0x1F
		v >>= 5
		if v > 0 {
			digit |= 0x20
		}
		out.WriteByte(alphabet[digit])
		if v == 0 {
			break
		}
	}

	return out.String()
}

// A production stack is unreadable until it is resolved. This is the whole point
// of the feature.
func Test_ResolveStack(t *testing.T) {
	maps := stubMaps{release: "streamlogia-frontend@1.5.0", fileName: "index-64s.js", consumer: buildMap(t)}

	// Column 20 is the second segment's own position. A column past the last
	// mapping on a line does not resolve at all — see the note in ResolveStack.
	stack := "TypeError: x is not a function\n" +
		"    at a (https://streamlogia.com/assets/index-64s.js:1:1)\n" +
		"    at o (https://streamlogia.com/assets/index-64s.js:1:20)"

	out, resolved := ResolveStack(context.Background(), stack, "streamlogia-frontend@1.5.0", maps)
	if !resolved {
		t.Fatalf("nothing was resolved:\n%s", out)
	}

	if !strings.Contains(out, "LogsView.jsx:142") {
		t.Errorf("the first frame was not resolved:\n%s", out)
	}
	if !strings.Contains(out, "handleFilterChange") {
		t.Errorf("the original function name is missing:\n%s", out)
	}
	if !strings.Contains(out, "LevelPicker.jsx:32") || !strings.Contains(out, "onChange") {
		t.Errorf("the second frame was not resolved:\n%s", out)
	}

	// The header line is not a frame and must survive untouched.
	if !strings.HasPrefix(out, "TypeError: x is not a function") {
		t.Errorf("the error line was rewritten:\n%s", out)
	}

	// A resolved stack has to parse back through the same frame parser the
	// fingerprint uses, or grouping would silently see no frames at all.
	frames := ParseStack(out)
	if len(frames) != 2 {
		t.Fatalf("the resolved stack parsed to %d frames, want 2:\n%s", len(frames), out)
	}
	if !strings.HasSuffix(frames[0].File, "LogsView.jsx") || frames[0].Function != "handleFilterChange" {
		t.Errorf("re-parsed frame = %+v", frames[0])
	}
}

// Everything that can go wrong with a map has to leave the stack usable.
func Test_ResolveStack_DegradesGracefully(t *testing.T) {
	maps := stubMaps{release: "streamlogia-frontend@1.5.0", fileName: "index-64s.js", consumer: buildMap(t)}

	stack := "    at a (https://streamlogia.com/assets/index-64s.js:1:1)"

	cases := []struct {
		name    string
		stack   string
		release string
		maps    SourceMapLookup
	}{
		{"no map for the release", stack, "streamlogia-frontend@9.9.9", maps},
		{"no map for the file", "    at a (https://streamlogia.com/assets/other-1a2.js:1:1)", "streamlogia-frontend@1.5.0", maps},
		{"no maps at all", stack, "streamlogia-frontend@1.5.0", nil},
		{"no release on the event", stack, "", maps},
		{"a stack with no frames", "TypeError: something went wrong", "streamlogia-frontend@1.5.0", maps},
		{"an empty stack", "", "streamlogia-frontend@1.5.0", maps},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, resolved := ResolveStack(context.Background(), c.stack, c.release, c.maps)

			if resolved {
				t.Errorf("reported a resolution it could not have made:\n%s", out)
			}
			if out != c.stack {
				t.Errorf("the stack was altered:\ngot  %q\nwant %q", out, c.stack)
			}
		})
	}

	// A frame at a position the map does not cover keeps its raw form while its
	// neighbours resolve — a partly resolved stack beats none.
	mixed := "    at a (https://streamlogia.com/assets/index-64s.js:1:1)\n" +
		"    at b (https://streamlogia.com/assets/vendor-8fa.js:2:400)"

	out, resolved := ResolveStack(context.Background(), mixed, "streamlogia-frontend@1.5.0", maps)
	if !resolved {
		t.Fatalf("nothing resolved in a mixed stack:\n%s", out)
	}
	if !strings.Contains(out, "LogsView.jsx") {
		t.Errorf("the mappable frame did not resolve:\n%s", out)
	}
	if !strings.Contains(out, "vendor-8fa.js:2:400") {
		t.Errorf("the unmappable frame was lost:\n%s", out)
	}
}

// The reason this matters as much as readability: a fingerprint built from
// minified frames changes at every deploy, so one bug becomes one issue per
// release. Resolved frames are stable.
func Test_Fingerprint_IsStableAcrossReleasesOnceResolved(t *testing.T) {
	oldMaps := stubMaps{release: "app@1.0.0", fileName: "index-aaa.js", consumer: buildMap(t)}
	newMaps := stubMaps{release: "app@1.1.0", fileName: "index-bbb.js", consumer: buildMap(t)}

	// The same crash, in two builds: different bundle name, different offsets.
	before := "    at a (https://streamlogia.com/assets/index-aaa.js:1:1)"
	after := "    at q (https://streamlogia.com/assets/index-bbb.js:1:1)"

	rawBefore, _, _ := Fingerprint(NewEvent{Kind: KindReact, Name: "TypeError", Message: "boom", Stack: before})
	rawAfter, _, _ := Fingerprint(NewEvent{Kind: KindReact, Name: "TypeError", Message: "boom", Stack: after})
	if rawBefore == rawAfter {
		t.Fatalf("the minified fingerprints matched by accident; the test proves nothing")
	}

	resolvedBefore, _ := ResolveStack(context.Background(), before, "app@1.0.0", oldMaps)
	resolvedAfter, _ := ResolveStack(context.Background(), after, "app@1.1.0", newMaps)

	fpBefore, _, culprit := Fingerprint(NewEvent{Kind: KindReact, Name: "TypeError", Message: "boom", Stack: resolvedBefore})
	fpAfter, _, _ := Fingerprint(NewEvent{Kind: KindReact, Name: "TypeError", Message: "boom", Stack: resolvedAfter})

	if fpBefore != fpAfter {
		t.Errorf("the same bug still fingerprints differently across releases:\n%s\n%s", resolvedBefore, resolvedAfter)
	}
	if !strings.Contains(culprit, "handleFilterChange") {
		t.Errorf("culprit = %q, want the original function name", culprit)
	}
}
