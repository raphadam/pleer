package proto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"errors"
	"fmt"
	"log"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

type RtcMessageType int

const (
	OPEN_TCP_REQUEST RtcMessageType = iota
	OPEN_TCP_SUCCESSFULL
	OPEN_TCP_FAILED
	CLOSE_TCP_FROM_LISTENER
	CLOSE_TCP_FROM_DIALER
	CLOSE_TCP_DIALER
	MESSAGE_TCP
	CLOSE_TCP
)

type RtcMessage struct {
	Type  RtcMessageType
	Data  []byte
	Error error
}

func RtcWrite(dc *webrtc.DataChannel, msg RtcMessage) error {
	buff := bytes.Buffer{}

	err := gob.NewEncoder(&buff).Encode(msg)
	if err != nil {
		return fmt.Errorf("unable to encode message %w", err)
	}

	err = dc.Send(buff.Bytes())
	if err != nil {
		return fmt.Errorf("unable to send message %w", err)
	}

	return nil
}

func RtcRead(data []byte) (RtcMessage, error) {
	var msg RtcMessage

	err := gob.NewDecoder(bytes.NewReader(data)).Decode(&msg)
	if err != nil {
		return RtcMessage{}, fmt.Errorf("error parsing wrtc %w", err)
	}

	return msg, nil
}

type ID string

type Message interface {
	IsMessage()
}

type Packet struct {
	M Message
}

func init() {
	gob.Register(&RtcMessage{})

	gob.Register(&Packet{})
	gob.Register(&HostAnnonceRequest{})
	gob.Register(&HostAnnonceResponse{})

	gob.Register(&GuestJoinRequest{})
	gob.Register(&GuestJoinOffer{})
	gob.Register(&GuestJoinAnswer{})
}

type GuestJoinAnswer struct {
	Party ID
	Guest ID
	SDP   webrtc.SessionDescription
	ICES  []*webrtc.ICECandidate
}

func (m *GuestJoinAnswer) IsMessage() {}

type GuestJoinOffer struct {
	Party ID
	Guest ID
	SDP   webrtc.SessionDescription
	ICES  []*webrtc.ICECandidate
}

func (m *GuestJoinOffer) IsMessage() {}

type GuestJoinRequest struct {
	Party ID
	Guest ID
}

func (m *GuestJoinRequest) IsMessage() {}

type HostAnnonceResponse struct {
	Succeed bool
}

func (m *HostAnnonceResponse) IsMessage() {}

type HostAnnonceRequest struct {
	Party ID
}

func (m *HostAnnonceRequest) IsMessage() {}

func NewID() ID {
	data := [32]byte{}

	_, err := rand.Read(data[:])
	if err != nil {
		log.Fatal("unable to generate random Id ", err)
	}

	id := base64.StdEncoding.EncodeToString(data[:])
	return ID(id)
}

type Conn struct {
	ws *websocket.Conn
}

func WrapConn(ws *websocket.Conn) *Conn {
	return &Conn{ws: ws}
}

func (c *Conn) WriteMessage(msg Message) error {
	buff := bytes.Buffer{}

	err := gob.NewEncoder(&buff).Encode(&Packet{
		M: msg,
	})
	if err != nil {
		return err
	}

	return c.ws.WriteMessage(websocket.BinaryMessage, buff.Bytes())
}

func (c *Conn) ReadMessage() (Message, error) {
	t, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, err
	}

	if t != websocket.BinaryMessage {
		return nil, errors.New("must be a binary message")
	}

	var p Packet

	err = gob.NewDecoder(bytes.NewReader(data)).Decode(&p)
	if err != nil {
		return nil, err
	}

	return p.M, nil
}

func (c *Conn) Close() error {
	err := c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		return err
	}

	return c.ws.Close()
}
