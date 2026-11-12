package mailbox

import (
	"bufio"
	"net"
	"strconv"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/commands"
	"github.com/emersion/go-imap/responses"
)

// protocolClient deliberately implements only the four read-only operations
// this adapter needs. The official parser is bounded before reading literals;
// unlike the general client constructor there is no logger or reader goroutine.
type protocolClient struct {
	conn          net.Conn
	reader        *imap.Reader
	writer        *imap.Writer
	sequence      uint64
	authenticated bool
}

func newProtocolClient(conn net.Conn) (*protocolClient, error) {
	r := imap.NewReader(bufio.NewReader(conn))
	r.MaxLiteralSize = MaxRawMessageBytes
	p := &protocolClient{conn: conn, reader: r, writer: imap.NewWriter(conn)}
	response, err := imap.ReadResp(r)
	if err != nil {
		return nil, fail(ProtocolError)
	}
	status, ok := response.(*imap.StatusResp)
	if !ok || status.Tag != "*" || status.Type != imap.StatusRespOk {
		return nil, fail(ProtocolError)
	}
	return p, nil
}

func (p *protocolClient) command(command imap.Commander, kind Kind, handle func(imap.Resp) error) (*imap.StatusResp, error) {
	p.sequence++
	cmd := command.Command()
	cmd.Tag = "M" + strconv.FormatUint(p.sequence, 10)
	if cmd.WriteTo(p.writer) != nil || p.writer.Flush() != nil {
		return nil, fail(Unreachable)
	}
	for count := 0; count < 2048; count++ {
		response, err := imap.ReadResp(p.reader)
		if err != nil {
			return nil, fail(ProtocolError)
		}
		if status, ok := response.(*imap.StatusResp); ok {
			if status.Tag == cmd.Tag {
				if status.Type != imap.StatusRespOk {
					return nil, fail(kind)
				}
				return status, nil
			}
			if status.Tag != "*" || status.Type == imap.StatusRespBye {
				return nil, fail(ProtocolError)
			}
		}
		if _, ok := response.(*imap.ContinuationReq); ok {
			return nil, fail(ProtocolError)
		}
		if handle != nil {
			if err := handle(response); err != nil {
				return nil, err
			}
		}
	}
	return nil, fail(ProtocolError)
}

func (p *protocolClient) Login(username, password string) error {
	_, err := p.command(&commands.Login{Username: username, Password: password}, AuthFailed, nil)
	if err == nil {
		p.authenticated = true
	}
	return err
}

func (p *protocolClient) identify() error {
	supportsID := false
	_, err := p.command(&commands.Capability{}, ProtocolError, func(response imap.Resp) error {
		if data, ok := response.(*imap.DataResp); ok {
			name, fields, ok := imap.ParseNamedResp(data)
			if ok && name == "CAPABILITY" {
				for _, field := range fields {
					value, ok := field.(string)
					if !ok {
						return fail(ProtocolError)
					}
					if strings.EqualFold(value, "ID") {
						supportsID = true
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !supportsID {
		return nil
	}
	// RFC 2971: only truthful app metadata; no OS, machine or user identifiers.
	_, err = p.command(&imap.Command{Name: "ID", Arguments: []interface{}{[]interface{}{"name", "Social Intelligence OS", "version", "0.1.0"}}}, ProtocolError, nil)
	return err
}

func (p *protocolClient) Select(folder string, readOnly bool) (*imap.MailboxStatus, error) {
	if !p.authenticated || !readOnly {
		return nil, fail(ProtocolError)
	}
	mbox := &imap.MailboxStatus{Name: folder, Items: make(map[imap.StatusItem]interface{})}
	selectResponse := &responses.Select{Mailbox: mbox}
	gotExists := false
	status, err := p.command(&commands.Select{Mailbox: folder, ReadOnly: true}, MailboxFailed, func(response imap.Resp) error {
		if data, ok := response.(*imap.DataResp); ok {
			name, fields, ok := imap.ParseNamedResp(data)
			if ok && name == "EXISTS" {
				if len(fields) != 1 {
					return fail(ProtocolError)
				}
				n, err := imap.ParseNumber(fields[0])
				if err != nil || gotExists {
					return fail(ProtocolError)
				}
				mbox.Messages = n
				gotExists = true
			}
		}
		if err := selectResponse.Handle(response); err != nil && err != responses.ErrUnhandled {
			return fail(ProtocolError)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mbox.ReadOnly = status.Code == imap.CodeReadOnly
	if !gotExists || !mbox.ReadOnly || mbox.UidValidity == 0 {
		return nil, fail(ProtocolError)
	}
	return mbox, nil
}

func (p *protocolClient) Fetch(set *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error {
	return p.fetch(set, items, ch, false)
}
func (p *protocolClient) UidFetch(set *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error {
	return p.fetch(set, items, ch, true)
}

func (p *protocolClient) fetch(set *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message, uid bool) error {
	defer close(ch)
	var command imap.Commander = &commands.Fetch{SeqSet: set, Items: items}
	if uid {
		command = &commands.Uid{Cmd: command}
	}
	_, err := p.command(command, ProtocolError, func(response imap.Resp) error {
		data, ok := response.(*imap.DataResp)
		if !ok {
			return nil
		}
		name, fields, ok := imap.ParseNamedResp(data)
		if !ok || name != "FETCH" {
			return nil
		}
		if len(fields) != 2 {
			return fail(ProtocolError)
		}
		seq, err := imap.ParseNumber(fields[0])
		if err != nil || seq == 0 {
			return fail(ProtocolError)
		}
		attrs, ok := fields[1].([]interface{})
		if !ok {
			return fail(ProtocolError)
		}
		message := &imap.Message{SeqNum: seq}
		if message.Parse(attrs) != nil {
			return fail(ProtocolError)
		}
		if uid {
			if message.Uid == 0 || !set.Contains(message.Uid) {
				return fail(ProtocolError)
			}
		} else if !set.Contains(seq) {
			return fail(ProtocolError)
		}
		ch <- message
		return nil
	})
	return err
}

func (p *protocolClient) Terminate() error { return p.conn.Close() }
