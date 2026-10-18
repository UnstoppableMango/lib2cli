// Package greet is the Go library the end-to-end test wraps. Its
// functions cover the idioms the shim reads from a signature.
package greet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Greet greets name.
func Greet(name string, excited bool) string {
	if excited {
		return "Hello, " + name + "!"
	}
	return "Hello, " + name
}

// Divide fails on a zero divisor.
func Divide(ctx context.Context, a, b float64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

// Stats counts what Count read.
type Stats struct {
	Lines int `json:"lines"`
	Bytes int `json:"bytes"`
}

// Count reads in to the end.
func Count(in io.Reader) (Stats, error) {
	data, err := io.ReadAll(in)
	if err != nil {
		return Stats{}, err
	}
	return Stats{Lines: bytes.Count(data, []byte("\n")), Bytes: len(data)}, nil
}

// Upper streams in back upper-cased.
func Upper(in io.Reader) io.Reader {
	data, _ := io.ReadAll(in)
	return bytes.NewReader(bytes.ToUpper(data))
}

// TagRequest is passed whole.
type TagRequest struct {
	Name   string
	Tags   []string
	Labels map[string]string
	Note   *string
}

// Tagged is what Tag returns.
type Tagged struct {
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

// Tag joins a request into one label.
func Tag(req TagRequest) Tagged {
	parts := append([]string{req.Name}, req.Tags...)
	for k, v := range req.Labels {
		parts = append(parts, k+"="+v)
	}
	t := Tagged{Label: strings.Join(parts, ",")}
	if req.Note != nil {
		t.Note = *req.Note
	}
	return t
}

// Ver is written with its String method.
type Ver struct{ Major, Minor int }

func (v Ver) String() string { return fmt.Sprintf("greet v%d.%d", v.Major, v.Minor) }

// Version reports the library's version.
func Version() Ver { return Ver{1, 0} }
