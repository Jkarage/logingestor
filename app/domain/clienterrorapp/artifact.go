package clienterrorapp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jkarage/logingestor/app/sdk/errs"
	"github.com/jkarage/logingestor/business/domain/clienterrorbus"
	"github.com/jkarage/logingestor/foundation/web"
)

// maxUploadBytes bounds the whole multipart request. It is above the per-file
// cap because a build emits several maps and CI may send them together.
const maxUploadBytes = 4 * clienterrorbus.MaxArtifactBytes

// UploadedArtifacts reports what an upload stored.
type UploadedArtifacts struct {
	Release   string             `json:"release"`
	Stored    int                `json:"stored"`
	Artifacts []UploadedArtifact `json:"artifacts"`
}

// UploadedArtifact is one stored map.
type UploadedArtifact struct {
	FileName    string `json:"fileName"`
	ByteSize    int    `json:"byteSize"`
	DateCreated string `json:"dateCreated"`
}

// Encode implements the encoder interface.
func (app UploadedArtifacts) Encode() ([]byte, string, error) {
	data, err := json.Marshal(app)
	return data, "application/json", err
}

// HTTPStatus returns 201 for a stored upload.
func (app UploadedArtifacts) HTTPStatus() int { return http.StatusCreated }

// uploadArtifacts handles POST /v1/client-errors/artifacts.
//
// This is a deploy-time call from CI, not from a user, so it authenticates with
// a single shared token rather than a session or an org-scoped key: there is no
// org involved — the maps describe our own frontend bundle, and one build serves
// every tenant.
func (a *app) uploadArtifacts(ctx context.Context, r *http.Request) web.Encoder {
	if resp := a.checkUploadToken(r); resp != nil {
		return resp
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(8 << 20); err != nil {
		// A body over the ceiling surfaces here, and it is a size problem rather
		// than a syntax one.
		if strings.Contains(err.Error(), "too large") {
			return errs.New(errs.PayloadTooLarge, errors.New("upload exceeds the size limit"))
		}
		return errs.New(errs.InvalidArgument, errors.New("expected a multipart form with release and files"))
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	release := strings.TrimSpace(r.FormValue("release"))
	if release == "" {
		return errs.New(errs.InvalidArgument, clienterrorbus.ErrArtifactRelease)
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		return errs.New(errs.InvalidArgument, errors.New("no files were uploaded under 'files'"))
	}

	out := UploadedArtifacts{Release: release, Artifacts: []UploadedArtifact{}}

	for _, header := range files {
		f, err := header.Open()
		if err != nil {
			return errs.Errorf(errs.Internal, "open upload %s: %s", header.Filename, err)
		}

		content, err := io.ReadAll(io.LimitReader(f, clienterrorbus.MaxArtifactBytes+1))
		_ = f.Close()
		if err != nil {
			return errs.Errorf(errs.Internal, "read upload %s: %s", header.Filename, err)
		}
		if len(content) > clienterrorbus.MaxArtifactBytes {
			return errs.Errorf(errs.PayloadTooLarge, "%s exceeds the per-file limit", header.Filename)
		}

		stored, err := a.clientErrorBus.UploadArtifact(ctx, clienterrorbus.NewArtifact{
			Release:    release,
			FileName:   header.Filename,
			Content:    content,
			UploadedBy: strings.TrimSpace(r.FormValue("uploadedBy")),
		})
		if err != nil {
			switch {
			case errors.Is(err, clienterrorbus.ErrArtifactInvalid),
				errors.Is(err, clienterrorbus.ErrArtifactEmpty),
				errors.Is(err, clienterrorbus.ErrArtifactRelease):
				return errs.Errorf(errs.InvalidArgument, "%s: %s", header.Filename, err)
			case errors.Is(err, clienterrorbus.ErrArtifactTooLarge):
				return errs.Errorf(errs.PayloadTooLarge, "%s: %s", header.Filename, err)
			}
			return errs.Errorf(errs.Internal, "uploadartifact %s: %s", header.Filename, err)
		}

		out.Artifacts = append(out.Artifacts, UploadedArtifact{
			FileName:    stored.FileName,
			ByteSize:    stored.ByteSize,
			DateCreated: time.Now().UTC().Format(time.RFC3339),
		})
	}

	out.Stored = len(out.Artifacts)

	return out
}

// queryArtifacts handles GET /v1/client-errors/artifacts?release=, so a deploy
// can be verified without waiting for a crash to test it on.
func (a *app) queryArtifacts(ctx context.Context, r *http.Request) web.Encoder {
	if resp := a.checkUploadToken(r); resp != nil {
		return resp
	}

	release := strings.TrimSpace(r.URL.Query().Get("release"))
	if release == "" {
		return errs.New(errs.InvalidArgument, clienterrorbus.ErrArtifactRelease)
	}

	stored, err := a.clientErrorBus.QueryArtifacts(ctx, release)
	if err != nil {
		return errs.Errorf(errs.Internal, "queryartifacts: %s", err)
	}

	out := UploadedArtifacts{Release: release, Stored: len(stored), Artifacts: make([]UploadedArtifact, len(stored))}
	for i, s := range stored {
		out.Artifacts[i] = UploadedArtifact{
			FileName:    s.FileName,
			ByteSize:    s.ByteSize,
			DateCreated: s.DateCreated.Format(time.RFC3339),
		}
	}

	return out
}

// checkUploadToken authenticates a CI caller.
//
// The comparison is constant time, and an unset token refuses everything rather
// than allowing everything — a deployment that forgot to configure this should
// fail to upload, not accept maps from anyone.
func (a *app) checkUploadToken(r *http.Request) web.Encoder {
	if a.uploadToken == "" {
		return errs.New(errs.PermissionDenied, errors.New("source map uploads are not configured"))
	}

	presented, err := bearer(r.Header.Get("authorization"))
	if err != nil {
		return errs.New(errs.Unauthenticated, err)
	}

	if subtle.ConstantTimeCompare([]byte(presented), []byte(a.uploadToken)) != 1 {
		return errs.New(errs.Unauthenticated, errors.New("invalid upload token"))
	}

	return nil
}

// bearer pulls the token out of an Authorization header.
func bearer(header string) (string, error) {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", errors.New("expected authorization header format: Bearer <token>")
	}

	return strings.TrimSpace(parts[1]), nil
}
