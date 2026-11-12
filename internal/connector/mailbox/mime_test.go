package mailbox

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var mimeDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func simpleMail(contentType, body string) []byte {
	return []byte("From: Alice <alice@example.com>\r\nTo: me@example.com\r\nSubject: A test\r\nDate: Thu, 01 Jan 2026 08:00:00 +0800\r\nMIME-Version: 1.0\r\nContent-Type: " + contentType + "\r\n\r\n" + body)
}

func TestMIMEEncodedHeadersAndChineseText(t *testing.T) {
	from := base64.StdEncoding.EncodeToString([]byte("张三"))
	subject := base64.StdEncoding.EncodeToString([]byte("会议安排"))
	body := base64.StdEncoding.EncodeToString([]byte("请查看材料。"))
	raw := []byte("From: =?UTF-8?B?" + from + "?= <alice@example.com>\r\nTo: me@example.com, Bob <bob@example.com>\r\nSubject: =?UTF-8?B?" + subject + "?=\r\nDate: Thu, 01 Jan 2026 08:00:00 +0800\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + body)
	mail, err := parseMail(raw, 77, 123, mimeDate, true)
	if err != nil {
		t.Fatal(err)
	}
	if mail.ExternalID != "77:123" || mail.UID != 123 || mail.FromName != "张三" || mail.FromAddress != "alice@example.com" || mail.Subject != "会议安排" || mail.Body != "请查看材料。" || mail.OccurredAt != "2026-01-01T00:00:00Z" || len(mail.To) != 2 || !mail.Seen {
		t.Fatalf("mail differs: %+v", mail)
	}
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中文正文"))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte("From: alice@example.com\r\nContent-Type: text/plain; charset=gbk\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(gbk))
	mail, err = parseMail(raw, 77, 124, mimeDate, false)
	if err != nil || mail.Body != "中文正文" || mail.Subject != "(无主题)" || mail.OccurredAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("GBK/date fallback failed: %+v %v", mail, err)
	}
	raw = []byte("From: alice@example.com\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n=E4=BC=9A=E8=AE=AE=0Aok")
	mail, err = parseMail(raw, 77, 125, mimeDate, false)
	if err != nil || mail.Body != "会议\nok" {
		t.Fatalf("quoted printable failed: %q %v", mail.Body, err)
	}
}

func TestMIMEPrefersPlainAndSkipsAttachments(t *testing.T) {
	body := "--outer\r\nContent-Type: multipart/alternative; boundary=alt\r\n\r\n--alt\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>HTML alternative</p>\r\n--alt\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n真实正文\r\n--alt--\r\n--outer\r\nContent-Type: text/plain; name=secret.txt\r\nContent-Disposition: attachment; filename=secret.txt\r\n\r\nATTACHMENT SECRET\r\n--outer--\r\n"
	mail, err := parseMail(simpleMail("multipart/mixed; boundary=outer", body), 1, 2, mimeDate, false)
	if err != nil || mail.Body != "真实正文" {
		t.Fatalf("attachment/alternative leaked: %+v %v", mail, err)
	}
	inline := strings.Replace(body, "Content-Disposition: attachment; filename=secret.txt", "Content-Disposition: inline; filename=secret.txt", 1)
	mail, err = parseMail(simpleMail("multipart/mixed; boundary=outer", inline), 1, 3, mimeDate, false)
	if err != nil || mail.Body != "真实正文" {
		t.Fatalf("inline attachment leaked: %+v %v", mail, err)
	}
	onlyAttachment := simpleMail("application/octet-stream", "binary that must not become mail text")
	mail, err = parseMail(onlyAttachment, 1, 4, mimeDate, false)
	if err != nil || mail.Body != "(无可显示的文本正文)" {
		t.Fatalf("binary became text: %+v %v", mail, err)
	}
}

func TestMIMEHTMLProducesTextWithoutExecutableOrRemoteContent(t *testing.T) {
	markup := `<html><head><title>HEAD SECRET</title><style>STYLE SECRET</style></head><body><p>Hello &amp; welcome</p><script>SCRIPT SECRET</script><iframe src="https://remote.invalid/private">IFRAME SECRET</iframe><div hidden><div>HIDDEN SECRET</div>STILL HIDDEN</div><div style="display: none">CSS SECRET</div><div aria-hidden="true">ARIA SECRET</div><svg><text>SVG SECRET</text></svg><template>TEMPLATE SECRET</template><p>Visible <a href="https://remote.invalid/secret">link</a><img src="https://remote.invalid/pixel.png" alt="IMAGE SECRET"></p></body></html>`
	mail, err := parseMail(simpleMail("text/html; charset=utf-8", markup), 1, 5, mimeDate, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mail.Body, "Hello & welcome") || !strings.Contains(mail.Body, "Visible link") {
		t.Fatalf("visible body lost: %q", mail.Body)
	}
	for _, secret := range []string{"SECRET", "STILL HIDDEN", "remote.invalid", "<script", "pixel.png"} {
		if strings.Contains(mail.Body, secret) {
			t.Fatalf("non-text content extracted: %q", mail.Body)
		}
	}
}

func TestMIMELimitsRejectWithoutPartialResult(t *testing.T) {
	tooDeep := "Content-Type: text/plain\r\n\r\nend"
	for depth := 0; depth < 22; depth++ {
		boundary := fmt.Sprintf("boundary-%d", depth)
		tooDeep = "Content-Type: multipart/mixed; boundary=" + boundary + "\r\n\r\n--" + boundary + "\r\n" + tooDeep + "\r\n--" + boundary + "--\r\n"
	}
	var many strings.Builder
	for i := 0; i < 101; i++ {
		many.WriteString("--many\r\nContent-Type: text/plain\r\n\r\nx\r\n")
	}
	many.WriteString("--many--\r\n")
	for name, raw := range map[string][]byte{
		"raw too big":      simpleMail("text/plain", strings.Repeat("x", MaxRawMessageBytes)),
		"body too big":     simpleMail("text/plain", strings.Repeat("界", MaxBodyBytes/3+1)),
		"subject too big":  []byte("From: alice@example.com\r\nSubject: " + strings.Repeat("界", 1025) + "\r\n\r\nx"),
		"nested multipart": []byte("From: alice@example.com\r\n" + tooDeep),
		"many parts":       simpleMail("multipart/mixed; boundary=many", many.String()),
	} {
		t.Run(name, func(t *testing.T) {
			mail, err := parseMail(raw, 1, 1, mimeDate, false)
			if err != errLimit || mail.ExternalID != "" || mail.Body != "" || mail.UID != 0 {
				t.Fatalf("limit did not fail closed: %+v %v", mail, err)
			}
		})
	}
}

func TestMIMERejectsInvalidIdentityEncodingAndMalformedInput(t *testing.T) {
	for name, raw := range map[string][]byte{
		"missing sender":                []byte("Subject: Test\r\n\r\nbody"),
		"multiple senders":              []byte("From: a@example.com, b@example.com\r\n\r\nbody"),
		"duplicate sender header":       []byte("From: a@example.com\r\nFrom: b@example.com\r\n\r\nbody"),
		"unknown body charset":          simpleMail("text/plain; charset=never-a-real-charset", "body"),
		"unknown header charset":        []byte("From: a@example.com\r\nSubject: =?never-a-real-charset?B?YQ==?=\r\n\r\nbody"),
		"unsupported transfer encoding": []byte("From: a@example.com\r\nContent-Transfer-Encoding: fictional\r\n\r\nbody"),
		"broken multipart":              simpleMail("multipart/mixed; boundary=missing", "no boundary here"),
		"NUL body":                      simpleMail("text/plain", "bad\x00body"),
		"invalid UTF8":                  append(simpleMail("text/plain", "bad"), 0xff),
	} {
		t.Run(name, func(t *testing.T) {
			mail, err := parseMail(raw, 1, 1, mimeDate, false)
			if err == nil || mail.ExternalID != "" || mail.Body != "" {
				t.Fatalf("invalid input accepted: %+v %v", mail, err)
			}
		})
	}
	if _, err := parseMail(simpleMail("text/plain", "body"), 0, 1, mimeDate, false); err == nil {
		t.Fatal("zero UIDVALIDITY accepted")
	}
	if _, err := parseMail([]byte("From: a@example.com\r\nDate: invalid\r\n\r\nbody"), 1, 1, time.Time{}, false); err == nil {
		t.Fatal("invalid header and internal date accepted")
	}
}
