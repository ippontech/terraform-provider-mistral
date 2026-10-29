// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// multipartForm is a multipart/form-data body, rebuilt for every attempt so
// that a retried upload sends the whole file again.
type multipartForm struct {
	fields []formField
	files  []formFile
}

type formField struct {
	name, value string
}

// formFile is a file part. open is called once per attempt, and the reader
// it returns is closed once the body is written.
type formFile struct {
	field       string
	filename    string
	contentType string
	open        func() (io.ReadCloser, error)
}

// reader opens the files, then streams the form through a pipe, so a large
// file is never held in memory, and returns its content type with the
// boundary. A file that cannot be opened fails before anything is sent.
func (f *multipartForm) reader() (io.Reader, string, error) {
	sources := make([]io.ReadCloser, 0, len(f.files))
	for _, file := range f.files {
		src, err := file.open()
		if err != nil {
			closeAll(sources)
			return nil, "", fmt.Errorf("open %s: %w", file.filename, err)
		}
		sources = append(sources, src)
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer closeAll(sources)
		pw.CloseWithError(f.write(mw, sources))
	}()
	return pr, mw.FormDataContentType(), nil
}

func closeAll(rs []io.ReadCloser) {
	for _, r := range rs {
		_ = r.Close()
	}
}

func (f *multipartForm) write(mw *multipart.Writer, sources []io.ReadCloser) error {
	for _, fld := range f.fields {
		if err := mw.WriteField(fld.name, fld.value); err != nil {
			return err
		}
	}
	for i, file := range f.files {
		if err := writeFile(mw, file, sources[i]); err != nil {
			return err
		}
	}
	return mw.Close()
}

func writeFile(mw *multipart.Writer, file formFile, src io.Reader) error {
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
		escapeQuotes(file.field), escapeQuotes(file.filename)))
	ct := file.contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, src)
	return err
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }
