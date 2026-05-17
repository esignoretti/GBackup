package restore

import "testing"

func TestExtractFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"drive/u@x.com/files/abc123_v5", "abc123"},
		{"drive/u@x.com/files/abc123_v5.xlsx", "abc123.xlsx"},
		{"drive/u@x.com/files/noversion", "noversion"},
		{"drive/u@x.com/files/doc_v1.docx", "doc.docx"},
	}
	for _, c := range cases {
		if got := extractFileName(c.in); got != c.want {
			t.Errorf("extractFileName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
