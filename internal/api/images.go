package api

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/s1n7ax/remote-agent/internal/domain"
)

// imagePart returns the "file" part of a multipart upload of one image. The
// body is capped a little above the image limit so an oversized upload is
// cut off early; the orchestrator enforces the exact limit.
func imagePart(w http.ResponseWriter, r *http.Request) (*multipart.Part, error) {
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxImageSize+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, badRequest("send the image as multipart/form-data")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, badRequest("no image in the request (form field \"file\")")
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, badRequest("each image can be at most 20 MB")
			}
			return nil, badRequest("bad multipart body: " + err.Error())
		}
		if part.FormName() == "file" {
			return part, nil
		}
		part.Close()
	}
}

func (s *Server) uploadImage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	part, err := imagePart(w, r)
	if err != nil {
		return err
	}
	defer part.Close()
	uid, err := s.Orch.UploadImage(r.Context(), id, part, part.FileName())
	if err != nil {
		return err
	}
	return created(w, map[string]string{"id": uid})
}

func (s *Server) addImage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	part, err := imagePart(w, r)
	if err != nil {
		return err
	}
	defer part.Close()
	im, err := s.Orch.AddImage(r.Context(), id, part, part.FileName())
	if err != nil {
		return err
	}
	return created(w, im)
}

func (s *Server) removeImage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		return badRequest("bad image number")
	}
	if err := s.Orch.RemoveImage(r.Context(), id, n); err != nil {
		return err
	}
	return ok(w, map[string]bool{"removed": true})
}
