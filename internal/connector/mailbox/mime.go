package mailbox

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	messageMail "github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

// parseMail only creates a plaintext view. It never follows an attachment path,
// a link or an image, and never interprets HTML as a browser document.
func parseMail(raw []byte, uidValidity, uid uint32, internalDate time.Time, seen bool) (Mail, error) {
	if len(raw) > MaxRawMessageBytes {
		return Mail{}, errLimit
	}
	if len(raw) == 0 || uidValidity == 0 || uid == 0 {
		return Mail{}, errUnsupported
	}
	entity, err := message.ReadWithOptions(bytes.NewReader(raw), &message.ReadOptions{MaxHeaderBytes: 32 << 10})
	if err != nil {
		return Mail{}, errUnsupported
	}
	header := messageMail.Header{Header: entity.Header}
	if countHeader(&entity.Header, "From") != 1 || countHeader(&entity.Header, "Subject") > 1 || countHeader(&entity.Header, "To") > 1 || countHeader(&entity.Header, "Date") > 1 {
		return Mail{}, errUnsupported
	}
	from, err := header.AddressList("From")
	if err != nil || len(from) != 1 || !validAddress(from[0].Address) {
		return Mail{}, errUnsupported
	}
	fromName, err := safeHeader(from[0].Name, 128)
	if err != nil {
		return Mail{}, err
	}
	subject, err := header.Subject()
	if err != nil {
		return Mail{}, errUnsupported
	}
	subject, err = safeHeader(subject, 1024)
	if err != nil {
		return Mail{}, err
	}
	if subject == "" {
		subject = "(无主题)"
	}
	to, err := header.AddressList("To")
	if err != nil {
		return Mail{}, errUnsupported
	}
	if len(to) > 100 {
		return Mail{}, errLimit
	}
	recipients := make([]string, 0, len(to))
	for _, recipient := range to {
		if !validAddress(recipient.Address) {
			return Mail{}, errUnsupported
		}
		recipients = append(recipients, recipient.Address)
	}
	at, dateErr := header.Date()
	if dateErr != nil || !validMailTime(at) {
		at = internalDate
	}
	if !validMailTime(at) {
		return Mail{}, errUnsupported
	}
	collector := mimeText{}
	if err := collector.walk(entity, 0); err != nil {
		return Mail{}, err
	}
	body, err := collector.body()
	if err != nil {
		return Mail{}, err
	}
	return Mail{ExternalID: fmt.Sprintf("%d:%d", uidValidity, uid), UID: uid, FromName: fromName, FromAddress: from[0].Address, To: recipients, Subject: subject, Body: body, OccurredAt: at.UTC().Format(time.RFC3339Nano), Seen: seen}, nil
}

func countHeader(header *message.Header, name string) int {
	fields := header.FieldsByKey(name)
	count := 0
	for fields.Next() {
		count++
	}
	return count
}

func validMailTime(at time.Time) bool {
	return !at.Before(time.Unix(0, 0)) && !at.After(time.Now().Add(24*time.Hour))
}

func validAddress(address string) bool {
	if len(address) < 3 || len(address) > 254 || !strings.Contains(address, "@") || !utf8.ValidString(address) {
		return false
	}
	for _, ch := range address {
		if unicode.IsControl(ch) || unicode.IsSpace(ch) {
			return false
		}
	}
	return true
}

func safeHeader(value string, limit int) (string, error) {
	if !utf8.ValidString(value) {
		return "", errUnsupported
	}
	if utf8.RuneCountInString(value) > limit {
		return "", errLimit
	}
	value = strings.Map(func(ch rune) rune {
		if unicode.IsControl(ch) {
			return ' '
		}
		return ch
	}, value)
	return strings.Join(strings.Fields(value), " "), nil
}

type mimeText struct {
	parts         int
	plain         []string
	html          []string
	plainTooLarge bool
	htmlTooLarge  bool
}

func (c *mimeText) walk(entity *message.Entity, depth int) error {
	c.parts++
	if depth > 20 || c.parts > 100 {
		return errLimit
	}
	contentType, params, err := entity.Header.ContentType()
	if err != nil {
		return errUnsupported
	}
	if disposition := entity.Header.Get("Content-Disposition"); disposition != "" {
		typeName, attributes, err := entity.Header.ContentDisposition()
		if err != nil {
			return errUnsupported
		}
		if strings.EqualFold(typeName, "attachment") || attributes["filename"] != "" {
			return nil
		}
	}
	if params["name"] != "" || params["filename"] != "" {
		return nil
	}
	if multipart := entity.MultipartReader(); multipart != nil {
		defer multipart.Close()
		hadPart := false
		for {
			part, err := multipart.NextPart()
			if err == io.EOF {
				if !hadPart {
					return errUnsupported
				}
				return nil
			}
			if err != nil {
				return errUnsupported
			}
			hadPart = true
			if err := c.walk(part, depth+1); err != nil {
				return err
			}
		}
	}
	if contentType != "text/plain" && contentType != "text/html" {
		return nil
	}
	limit := MaxBodyBytes
	if contentType == "text/html" {
		limit = MaxRawMessageBytes
	}
	raw, err := io.ReadAll(io.LimitReader(entity.Body, int64(limit+1)))
	if err != nil {
		return errUnsupported
	}
	if len(raw) > limit {
		if contentType == "text/plain" {
			c.plainTooLarge = true
		} else {
			c.htmlTooLarge = true
		}
		return nil
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return errUnsupported
	}
	text := strings.TrimSpace(string(raw))
	if contentType == "text/plain" {
		c.plain = append(c.plain, text)
	} else {
		text, err = htmlText(text)
		if err != nil {
			if err == errLimit {
				c.htmlTooLarge = true
				return nil
			}
			return err
		}
		c.html = append(c.html, text)
	}
	return nil
}

func (c *mimeText) body() (string, error) {
	body := strings.TrimSpace(strings.Join(c.plain, "\n\n"))
	if c.plainTooLarge {
		return "", errLimit
	}
	if body == "" {
		if c.htmlTooLarge {
			return "", errLimit
		}
		body = strings.TrimSpace(strings.Join(c.html, "\n\n"))
	}
	if body == "" {
		body = "(无可显示的文本正文)"
	}
	if len(body) > MaxBodyBytes {
		return "", errLimit
	}
	return body, nil
}

// htmlText uses tokens rather than regular expressions, excludes non-visible
// containers and ignores all attributes (including URLs). It emits text only.
func htmlText(source string) (string, error) {
	decoder := html.NewTokenizer(strings.NewReader(source))
	decoder.SetMaxBuf(MaxRawMessageBytes)
	var output strings.Builder
	var suppressed []string
	for {
		kind := decoder.Next()
		switch kind {
		case html.ErrorToken:
			if decoder.Err() != io.EOF {
				return "", errUnsupported
			}
			return strings.TrimSpace(output.String()), nil
		case html.TextToken:
			if len(suppressed) > 0 {
				continue
			}
			text := strings.Join(strings.Fields(string(decoder.Text())), " ")
			if text != "" {
				if output.Len() > 0 && !strings.HasSuffix(output.String(), "\n") {
					output.WriteByte(' ')
				}
				output.WriteString(text)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := decoder.Token()
			blocked := hiddenHTMLTag(token.Data)
			for _, attribute := range token.Attr {
				if attribute.Key == "hidden" || attribute.Key == "aria-hidden" && strings.EqualFold(attribute.Val, "true") {
					blocked = true
				}
				if attribute.Key == "style" {
					css := strings.ToLower(strings.Join(strings.Fields(attribute.Val), ""))
					if strings.Contains(css, "display:none") || strings.Contains(css, "visibility:hidden") {
						blocked = true
					}
				}
			}
			if kind == html.StartTagToken && (blocked || len(suppressed) > 0) && !voidHTMLTag(token.Data) {
				suppressed = append(suppressed, token.Data)
			}
			if len(suppressed) == 0 && blockHTMLTag(token.Data) {
				writeBreak(&output)
			}
		case html.EndTagToken:
			token := decoder.Token()
			if len(suppressed) > 0 {
				if suppressed[len(suppressed)-1] == token.Data {
					suppressed = suppressed[:len(suppressed)-1]
				}
				continue
			}
			if blockHTMLTag(token.Data) {
				writeBreak(&output)
			}
		}
		if output.Len() > MaxBodyBytes {
			return "", errLimit
		}
	}
}

func writeBreak(output *strings.Builder) {
	if output.Len() > 0 && !strings.HasSuffix(output.String(), "\n") {
		output.WriteByte('\n')
	}
}
func hiddenHTMLTag(name string) bool {
	switch name {
	case "script", "style", "head", "iframe", "object", "svg", "template", "noscript", "canvas":
		return true
	}
	return false
}
func blockHTMLTag(name string) bool {
	switch name {
	case "br", "p", "div", "section", "article", "header", "footer", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
		return true
	}
	return false
}
func voidHTMLTag(name string) bool {
	switch name {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}
