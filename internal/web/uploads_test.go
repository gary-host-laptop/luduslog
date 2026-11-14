package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"luduslog/internal/config"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func multipartImage(t *testing.T, field string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, "image.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest("POST", "/settings/profile", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestImageUploadValidation(t *testing.T) {
	s := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	pngData := testPNG(t, 200, 100)
	up, err := s.readImageUpload(multipartImage(t, "avatar_upload", pngData), "avatar_upload", "avatar")
	if err != nil || !up.set {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(up.data))
	if err != nil || img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 {
		t.Fatalf("avatar was not resized: %v %v", err, img.Bounds())
	}
	header, err := s.readImageUpload(multipartImage(t, "header_upload", pngData), "header_upload", "header")
	if err != nil || !header.set || header.ext != ".jpg" {
		t.Fatalf("valid header rejected: %+v %v", header, err)
	}
	if _, err := s.readImageUpload(multipartImage(t, "avatar_upload", []byte("not an image")), "avatar_upload", "avatar"); err == nil {
		t.Fatal("non-image upload was accepted")
	}
	tooBig := bytes.Repeat([]byte("x"), maxUploadBytes+1)
	if _, err := s.readImageUpload(multipartImage(t, "avatar_upload", tooBig), "avatar_upload", "avatar"); err == nil {
		t.Fatal("oversized upload was accepted")
	}
	if _, err := s.readImageUpload(multipartImage(t, "avatar_upload", []byte("")), "avatar_upload", "avatar"); err == nil {
		t.Fatal("empty upload was accepted")
	}
}

func TestUploadPathsAndCleanup(t *testing.T) {
	s := &Server{cfg: config.Config{DataDir: t.TempDir()}}
	rel, err := s.saveUpload(imageUpload{data: testPNG(t, 10, 10), ext: ".png"}, "avatar", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !isLocalUpload(rel, "avatar", 7) {
		t.Fatalf("saved upload not recognized: %s", rel)
	}
	full := filepath.Join(s.cfg.DataDir, "uploads", "avatars", "7", filepath.Base(rel))
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("saved upload missing: %v", err)
	}
	for _, bad := range []string{
		"/uploads/avatars/8/extra.png",
		"/uploads/avatars/7/../8/extra.png",
		"/uploads/avatars/7/sub/extra.png",
		"/uploads/headers/7/extra.png",
		"/uploads/avatars/7/",
	} {
		if isLocalUpload(bad, "avatar", 7) {
			t.Errorf("unsafe upload path accepted: %s", bad)
		}
	}
	s.deleteUpload(rel, "avatar", 7)
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("old upload was not deleted: %v", err)
	}
	// External URLs are never passed to os.Remove.
	s.deleteUpload("https://example.test/avatar.png", "avatar", 7)
}
