package web

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	stddraw "image/draw"
	"image/jpeg"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxUploadBytes = 5 << 20

type imageUpload struct {
	data []byte
	ext  string
	set  bool
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	rel := path.Clean("/" + r.PathValue("path"))
	if rel == "/" || strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.Abs(filepath.Join(s.cfg.DataDir, "uploads"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, "/")))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(full); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, full)
}

func (s *Server) readImageUpload(r *http.Request, field, kind string) (imageUpload, error) {
	file, _, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return imageUpload{}, nil
	}
	if err != nil {
		return imageUpload{}, fmt.Errorf("choose an image file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		return imageUpload{}, err
	}
	if len(data) > maxUploadBytes {
		return imageUpload{}, fmt.Errorf("%s image must be 5 MiB or smaller", kind)
	}
	if len(data) == 0 {
		return imageUpload{}, fmt.Errorf("the %s image is empty", kind)
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return imageUpload{}, fmt.Errorf("%s must be a PNG, JPEG, or WebP image", kind)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return imageUpload{}, fmt.Errorf("could not decode the %s image", kind)
	}
	if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return imageUpload{}, fmt.Errorf("the %s image dimensions are not supported", kind)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return imageUpload{}, fmt.Errorf("could not decode the %s image", kind)
	}
	encoded, ext, err := encodeUpload(src, kind)
	if err != nil {
		return imageUpload{}, err
	}
	return imageUpload{data: encoded, ext: ext, set: true}, nil
}

func encodeUpload(src image.Image, kind string) ([]byte, string, error) {
	maxW, maxH := 512, 512
	if kind == "header" {
		maxW, maxH = 1500, 500
	}
	aspect := float64(maxW) / float64(maxH)
	crop := cropForAspect(src.Bounds(), aspect)
	var buf bytes.Buffer
	if kind == "avatar" {
		dst := image.NewNRGBA(image.Rect(0, 0, maxW, maxH))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, xdraw.Over, nil)
		if err := png.Encode(&buf, dst); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), ".png", nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, maxW, maxH))
	stddraw.Draw(dst, dst.Bounds(), image.White, image.Point{}, stddraw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, xdraw.Over, nil)
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ".jpg", nil
}

func cropForAspect(bounds image.Rectangle, aspect float64) image.Rectangle {
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return bounds
	}
	srcAspect := float64(w) / float64(h)
	if srcAspect > aspect {
		nw := int(float64(h) * aspect)
		if nw < 1 {
			nw = 1
		}
		x := bounds.Min.X + (w-nw)/2
		return image.Rect(x, bounds.Min.Y, x+nw, bounds.Max.Y)
	}
	nh := int(float64(w) / aspect)
	if nh < 1 {
		nh = 1
	}
	y := bounds.Min.Y + (h-nh)/2
	return image.Rect(bounds.Min.X, y, bounds.Max.X, y+nh)
}

func (s *Server) saveUpload(up imageUpload, kind string, userID int64) (string, error) {
	dir := filepath.Join(s.cfg.DataDir, "uploads", kind+"s", strconv.FormatInt(userID, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	name := randomID(16) + up.ext
	if err := os.WriteFile(filepath.Join(dir, name), up.data, 0o640); err != nil {
		return "", err
	}
	return "/uploads/" + kind + "s/" + strconv.FormatInt(userID, 10) + "/" + name, nil
}

// isLocalUpload reports whether rel is inside this user's directory for the
// given image kind. It rejects traversal and paths from other users.
func isLocalUpload(rel, kind string, userID int64) bool {
	prefix := "/uploads/" + kind + "s/" + strconv.FormatInt(userID, 10) + "/"
	if !strings.HasPrefix(rel, prefix) {
		return false
	}
	name := strings.TrimPrefix(rel, prefix)
	return name != "" && !strings.Contains(name, "/") && !strings.Contains(name, "\\") && name != "." && name != ".."
}

// deleteUserUploads removes a deleted account's avatar and header directories.
// It runs after the database commit and is best-effort: the account is already
// gone, so a failed cleanup is logged but not surfaced as a failed deletion.
func (s *Server) deleteUserUploads(userID int64) {
	if userID <= 0 {
		return
	}
	root, err := filepath.Abs(filepath.Join(s.cfg.DataDir, "uploads"))
	if err != nil {
		s.log.Warn("delete user uploads", "user", userID, "err", err)
		return
	}
	for _, kind := range []string{"avatar", "header"} {
		dir := filepath.Join(root, kind+"s", strconv.FormatInt(userID, 10))
		if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			s.log.Warn("delete user uploads", "user", userID, "kind", kind, "err", err)
		}
	}
}

func (s *Server) deleteUpload(rel, kind string, userID int64) {
	if !isLocalUpload(rel, kind, userID) {
		return
	}
	name := strings.TrimPrefix(rel, "/uploads/"+kind+"s/"+strconv.FormatInt(userID, 10)+"/")
	_ = os.Remove(filepath.Join(s.cfg.DataDir, "uploads", kind+"s", strconv.FormatInt(userID, 10), name))
}
