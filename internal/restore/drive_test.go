package restore

import "testing"

func TestExtractFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"drive/u@x.com/files/abc123_v5", "abc123"},
		{"drive/u@x.com/files/file_v3.pdf_v2", "file_v3.pdf"},
		{"drive/u@x.com/files/noversion", "noversion"},
	}
	for _, c := range cases {
		if got := extractFileName(c.in); got != c.want {
			t.Errorf("extractFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
