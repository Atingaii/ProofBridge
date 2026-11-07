package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

func writeSMTPMessage(dst io.Writer, from, to *mail.Address, m Message) error {
	sum := sha256.Sum256([]byte(m.ID))
	domain := "proofbridge.local"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}
	var alternative *multipart.Writer
	contentType := "text/plain; charset=UTF-8"
	if m.HTML != "" {
		alternative = multipart.NewWriter(dst)
		contentType = mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alternative.Boundary()})
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <proofbridge-%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: %s\r\n", from.String(), to.String(), mime.QEncoding.Encode("UTF-8", m.Subject), time.Now().UTC().Format(time.RFC1123Z), hex.EncodeToString(sum[:16]), domain, contentType)
	if alternative == nil {
		headers += "Content-Transfer-Encoding: quoted-printable\r\n"
	}
	if _, err := io.WriteString(dst, headers+"\r\n"); err != nil {
		return err
	}
	writePart := func(w io.Writer, body string) error {
		encoded := quotedprintable.NewWriter(w)
		if _, err := io.WriteString(encoded, body); err != nil {
			return err
		}
		return encoded.Close()
	}
	if alternative == nil {
		return writePart(dst, m.Text)
	}
	for _, part := range []struct{ kind, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		header := textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=UTF-8"}, "Content-Transfer-Encoding": {"quoted-printable"}}
		writer, err := alternative.CreatePart(header)
		if err != nil {
			return err
		}
		if err = writePart(writer, part.body); err != nil {
			return err
		}
	}
	return alternative.Close()
}
