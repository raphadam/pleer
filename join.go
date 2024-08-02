package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/raphadam/pleer/proto"
)

func Join(partyID string) {
	dialer := websocket.DefaultDialer

	c, _, err := dialer.Dial("ws://192.168.0.40:1323", nil)
	if err != nil {
		log.Fatal("unable to connec to signal server")
	}
	ws := proto.WrapConn(c)

	// define your own id
	guestID := proto.NewID()

	// display
	fmt.Println()
	fmt.Println(Bold + White + BlackB + "| Pleer |" + Reset + Bold + " Forward your ports " + Underline + "peer" + NoUnderline + " to " + Underline + "peer" + NoUnderline + ".")
	fmt.Printf(Reset + Italic + "You joined a party.\n\n")

	fmt.Println(Reset + Bold + Black + WhiteB + "Party ID:" + Reset + " " + partyID)
	fmt.Println(Reset + Bold + Black + WhiteB + "Your  ID:" + Reset + " " + guestID + "\n")

	err = ws.WriteMessage(&proto.GuestJoinRequest{Party: proto.ID(partyID), Guest: guestID})
	if err != nil {
		log.Fatal("unable to write annonce", err)
	}

	msg, err := ws.ReadMessage()
	if err != nil {
		log.Fatal("error trying to read message", err)
	}

	gjo, ok := msg.(*proto.GuestJoinOffer)
	if !ok {
		log.Fatal("failed to annonce itself")
	}

	peer, err := NewPeerGuest()
	if err != nil {
		log.Fatal(err)
	}

	sdp, ices, err := peer.CreateAnswer(gjo.SDP, gjo.ICES)
	if err != nil {
		log.Fatal("unable to create answer ", err)
	}

	err = ws.WriteMessage(&proto.GuestJoinAnswer{
		Party: gjo.Party,
		Guest: guestID,
		SDP:   sdp,
		ICES:  ices,
	})
	if err != nil {
		log.Fatal("unable to write guest answer", err)
	}

	err = ws.Close()
	if err != nil {
		log.Fatal("unable to close the ws", err)
	}

	select {}
}

type PeerGuest struct {
	conn    *webrtc.PeerConnection
	ices    []*webrtc.ICECandidate
	icesMux sync.Mutex
}

func NewPeerGuest() (*PeerGuest, error) {
	peerConn, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create a peer connection %w", err)
	}

	p := &PeerGuest{
		conn: peerConn,
	}
	p.conn.OnICECandidate(p.OnICECandidate)
	p.conn.OnConnectionStateChange(p.OnConnectionStateChange)
	p.conn.OnDataChannel(p.OnDataChannel)

	return p, nil
}

func (p *PeerGuest) CreateAnswer(sdp webrtc.SessionDescription, ices []*webrtc.ICECandidate) (webrtc.SessionDescription, []*webrtc.ICECandidate, error) {
	err := p.conn.SetRemoteDescription(sdp)
	if err != nil {
		return webrtc.SessionDescription{}, nil, fmt.Errorf("unable to define remote desc %w", err)
	}

	answer, err := p.conn.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, nil, fmt.Errorf("unable to create answer %w", err)
	}

	err = p.conn.SetLocalDescription(answer)
	if err != nil {
		return webrtc.SessionDescription{}, nil, fmt.Errorf("unable to define local description %w", err)
	}

	<-webrtc.GatheringCompletePromise(p.conn)

	for _, ice := range ices {
		err := p.conn.AddICECandidate(ice.ToJSON())
		if err != nil {
			return webrtc.SessionDescription{}, nil, fmt.Errorf("unable to add ice candidate %w", err)
		}
	}

	return answer, p.ices, nil
}

func (p *PeerGuest) OnICECandidate(c *webrtc.ICECandidate) {
	if c == nil {
		return
	}

	p.icesMux.Lock()
	p.ices = append(p.ices, c)
	p.icesMux.Unlock()
}

func (p *PeerGuest) OnConnectionStateChange(pcs webrtc.PeerConnectionState) {
	// fmt.Printf("Peer Connection State has changed: %s\n", pcs.String())
}

func (p *PeerGuest) OnDataChannel(dc *webrtc.DataChannel) {
	label := dc.Label()
	splits := strings.Split(label, ":")

	if len(splits) != 2 {
		log.Fatal("must provide this format proto:port")
	}

	proto := splits[0]
	port := splits[1]

	switch proto {
	case "tcp":
		_, err := p.CreateTcpChannel(dc, port)
		if err != nil {
			log.Fatal("unable to create tcp datachannel")
		}

	default:
		log.Fatal("must provide either tcp nor udp")
	}

}

func (p *PeerGuest) CreateTcpChannel(dc *webrtc.DataChannel, port string) (*webrtc.DataChannel, error) {
	crecv := make(chan proto.RtcMessage)
	listener := Listener{}

	dc.OnOpen(func() {
		csend, err := listener.Init(context.Background(), port, crecv)
		if err != nil {
			log.Fatal("unable to start the listener", err)
		}

		for msg := range csend {
			err := proto.RtcWrite(dc, msg)
			if err != nil {
				log.Fatal("unable to write rtc messsage", err)
			}
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		m, err := proto.RtcRead(msg.Data)
		if err != nil {
			log.Println("unable to read message")
		}
		crecv <- m
		// log.Println("data channel on message", m)
	})

	dc.OnError(func(err error) {
		log.Println("data channel on error", err)
	})

	dc.OnClose(func() {
		log.Println("data channel on close")
	})
	return nil, nil
}

type NetMessageType int

const (
	CONN_ACCEPTED NetMessageType = iota
	READ_MESSAGE
	READ_CLOSE
)

type NetMessage struct {
	Type  NetMessageType
	Conn  net.Conn
	Data  []byte
	Error error
}

type ListenerState int

const (
	TCP_DISCONNECTED ListenerState = iota
	TCP_ESTABLISHING
	TCP_CONNECTED
)

type Listener struct {
	state      ListenerState
	nl         net.Listener
	ctx        context.Context
	csend      chan proto.RtcMessage
	crecv      <-chan proto.RtcMessage
	toAccept   chan struct{}
	toRead     chan struct{}
	fromAccept <-chan NetMessage
	fromRead   <-chan NetMessage
	conn       net.Conn
}

func (l *Listener) Init(ctx context.Context, port string, crecv <-chan proto.RtcMessage) (<-chan proto.RtcMessage, error) {
	nl, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return nil, fmt.Errorf("unable to listen %w", err)
	}

	l.nl = nl
	l.ctx = ctx

	l.crecv = crecv
	l.csend = make(chan proto.RtcMessage)

	l.toAccept = make(chan struct{})
	l.toRead = make(chan struct{})

	l.fromAccept = HandleAccept(ctx, l.nl, l.toAccept)
	l.toAccept <- struct{}{}

	l.state = TCP_DISCONNECTED
	go l.Serve()

	return l.csend, nil
}

func (l *Listener) Serve() {
	for {
		select {
		case msg := <-l.fromAccept:
			l.OnAcceptMessage(msg)

		case msg := <-l.crecv:
			l.OnRtcMessage(msg)

		case msg := <-l.fromRead:
			l.OnReadMessage(msg)
		}
	}
}

func (l *Listener) OnReadMessage(msg NetMessage) {
	switch l.state {
	case TCP_CONNECTED:

		switch msg.Type {
		case READ_MESSAGE:
			l.csend <- proto.RtcMessage{Type: proto.MESSAGE_TCP, Data: msg.Data}

		case READ_CLOSE:
			l.state = TCP_DISCONNECTED

			l.fromRead = nil
			l.conn = nil
			l.csend <- proto.RtcMessage{Type: proto.CLOSE_TCP_FROM_LISTENER}
			l.toAccept <- struct{}{}

		default:
			log.Fatal("not impl")
		}

	default:
		log.Fatal("not good!!!")
	}

}

func (l *Listener) OnAcceptMessage(msg NetMessage) {
	switch l.state {
	case TCP_DISCONNECTED:

		switch msg.Type {
		case CONN_ACCEPTED:
			l.state = TCP_ESTABLISHING

			l.conn = msg.Conn
			l.fromRead = HandleR(l.ctx, l.conn, l.toRead)
			l.csend <- proto.RtcMessage{Type: proto.OPEN_TCP_REQUEST}

		default:
			log.Fatal("not poss")
		}

	default:
		log.Fatal("must not be in that state")
	}
}

func (l *Listener) OnRtcMessage(msg proto.RtcMessage) {
	switch l.state {
	case TCP_ESTABLISHING:

		switch msg.Type {
		case proto.OPEN_TCP_SUCCESSFULL:
			l.state = TCP_CONNECTED

			l.toRead <- struct{}{}

		case proto.OPEN_TCP_FAILED:
			l.state = TCP_DISCONNECTED

			l.toRead <- struct{}{}
			l.conn.Close()
			<-l.fromRead

			l.conn = nil
			l.fromRead = nil
			l.toAccept <- struct{}{}

		default:
			log.Fatal("not handled")
		}

	case TCP_CONNECTED:

		switch msg.Type {
		case proto.MESSAGE_TCP:
			_, err := l.conn.Write(msg.Data)
			if err != nil {
				log.Fatal("unable to forward message to listener")
			}

		case proto.CLOSE_TCP_FROM_DIALER:
			l.state = TCP_DISCONNECTED

			l.conn.Close()
			<-l.fromRead

			l.conn = nil
			l.fromRead = nil
			l.toAccept <- struct{}{}

		default:
			log.Fatal("noaaaa")
		}

	default:
		log.Fatal("impossible")
	}
}

func HandleAccept(ctx context.Context, l net.Listener, toAccept <-chan struct{}) <-chan NetMessage {
	cres := make(chan NetMessage)

	go func() {
		defer close(cres)

		for range toAccept {
			conn, err := l.Accept()
			if err != nil {
				log.Fatal("unable to accept conn", err)
				continue
			}

			cres <- NetMessage{Type: CONN_ACCEPTED, Conn: conn}
		}
	}()

	return cres
}

func HandleR(ctx context.Context, conn net.Conn, toRead <-chan struct{}) <-chan NetMessage {
	cres := make(chan NetMessage)
	buff := [2048]byte{}

	go func() {
		defer close(cres)
		<-toRead

		for {
			n, err := conn.Read(buff[:])
			if err != nil {
				cres <- NetMessage{Type: READ_CLOSE, Error: err}
				return
			}

			data := slices.Clone(buff[:n])
			cres <- NetMessage{Type: READ_MESSAGE, Data: data}
		}
	}()

	return cres
}
