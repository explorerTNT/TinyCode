package tools

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

var (
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEBOM = []byte{0xFF, 0xFE}
	utf16BEBOM = []byte{0xFE, 0xFF}
)

type textEncoding int

const (
	encUTF8 textEncoding = iota
	encUTF8BOM
	encUTF16LE
	encUTF16BE
	encCP1251
)

func (e textEncoding) String() string {
	switch e {
	case encUTF8BOM:
		return "UTF-8 with BOM"
	case encUTF16LE:
		return "UTF-16 LE"
	case encUTF16BE:
		return "UTF-16 BE"
	case encCP1251:
		return "Windows-1251"
	}
	return "UTF-8"
}

// decodeText turns file bytes into a string, recognising UTF-8 (with or
// without BOM), UTF-16 (with BOM, or by its pattern of recurring high bytes)
// and, as the last resort, Windows-1251, which legacy Russian sources still
// use.
func decodeText(b []byte) (string, textEncoding) {
	switch {
	case bytes.HasPrefix(b, utf8BOM):
		return string(b[len(utf8BOM):]), encUTF8BOM
	case bytes.HasPrefix(b, utf16LEBOM):
		return decodeUTF16(b[2:], unicode.LittleEndian), encUTF16LE
	case bytes.HasPrefix(b, utf16BEBOM):
		return decodeUTF16(b[2:], unicode.BigEndian), encUTF16BE
	}
	// BOM-less UTF-16 of a non-Latin script is also valid UTF-8 (plain
	// control bytes), so it has to be recognised first. Its spaces and line
	// breaks still carry NUL bytes, which real text never does.
	if bytes.IndexByte(b, 0) >= 0 {
		if looksUTF16(b, 1) {
			return decodeUTF16(b, unicode.LittleEndian), encUTF16LE
		}
		if looksUTF16(b, 0) {
			return decodeUTF16(b, unicode.BigEndian), encUTF16BE
		}
	}
	if utf8.Valid(b) {
		return string(b), encUTF8
	}
	if dec, err := charmap.Windows1251.NewDecoder().Bytes(b); err == nil {
		return string(dec), encCP1251
	}
	return strings.ToValidUTF8(string(b), "\uFFFD"), encUTF8
}

func decodeUTF16(b []byte, endian unicode.Endianness) string {
	dec, err := unicode.UTF16(endian, unicode.IgnoreBOM).NewDecoder().Bytes(b)
	if err != nil {
		return strings.ToValidUTF8(string(b), "\uFFFD")
	}
	return string(dec)
}

// looksUTF16 reports whether the bytes at index highAt of the 16-bit units
// look like the high half of UTF-16 text: a handful of recurring values,
// such as 0x00 for Latin text or 0x04 for Cyrillic. Single-byte encodings
// and binary data spread over many more.
func looksUTF16(b []byte, highAt int) bool {
	if len(b) < 8 {
		return false
	}
	n := len(b)
	if n > 512 {
		n = 512
	}
	n -= n % 2

	counts := map[byte]int{}
	for i := 0; i < n; i += 2 {
		counts[b[i+highAt]]++
	}
	dominant := 0
	for _, c := range counts {
		if c > dominant {
			dominant = c
		}
	}
	units := n / 2
	return len(counts) <= 8 && dominant*10 >= units*6
}

// encodeText reverses decodeText. It fails when the text has characters the
// encoding cannot store.
func encodeText(s string, enc textEncoding) ([]byte, error) {
	switch enc {
	case encUTF8BOM:
		return append(append([]byte{}, utf8BOM...), s...), nil
	case encUTF16LE:
		return encodeUTF16(s, unicode.LittleEndian, utf16LEBOM)
	case encUTF16BE:
		return encodeUTF16(s, unicode.BigEndian, utf16BEBOM)
	case encCP1251:
		return charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	}
	return []byte(s), nil
}

func encodeUTF16(s string, endian unicode.Endianness, bom []byte) ([]byte, error) {
	enc, err := unicode.UTF16(endian, unicode.IgnoreBOM).NewEncoder().Bytes([]byte(s))
	if err != nil {
		return nil, err
	}
	return append(append([]byte{}, bom...), enc...), nil
}

// errUnencodable explains why text was refused instead of being stored in
// the file's encoding.
func errUnencodable(enc textEncoding, err error) error {
	return fmt.Errorf("the file is %s and cannot store all of this text (%v). Use characters that exist in that encoding", enc, err)
}
