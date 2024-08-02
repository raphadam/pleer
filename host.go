package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"slices"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/raphadam/pleer/proto"
)

const (
	Reset = "\033[0m"
	Bold  = "\033[1m"

	Underline   = "\033[4m"
	NoUnderline = "\033[24m"

	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	White  = "\033[37m"
	Black  = "\033[30m"

	Italic = "\033[3m"

	BlackB  = "\033[40m"
	RedB    = "\033[41m"
	GreenB  = "\033[42m"
	YellowB = "\033[43m"
	BlueB   = "\033[44m"
	WhiteB  = "\033[47m"
)

func Host(tcps []int, udps []int) {
	dialer := websocket.DefaultDialer

	c, _, err := dialer.Dial("ws://localhost:1323", nil)
	if err != nil {
		log.Fatal("unable to connec to signal server")
	}
	ws := proto.WrapConn(c)

	// define your own id
	partyID := proto.NewID()

	// display
	fmt.Println()
	fmt.Println(Bold + White + BlackB + "| Pleer |" + Reset + Bold + " Forward your ports " + Underline + "peer" + NoUnderline + " to " + Underline + "peer" + NoUnderline + ".")
	fmt.Printf(Reset + Italic + "You are currently hosting a party.\n\n")

	fmt.Println(Reset + Bold + Black + WhiteB + "Party ID:" + Reset + " " + partyID + "\n")
	fmt.Println(Reset + Bold + Black + WhiteB + "Ports forwarded:" + Reset)

	for _, tcp := range tcps {
		fmt.Printf("-> TCP:%d\n", tcp)
	}

	err = ws.WriteMessage(&proto.HostAnnonceRequest{Party: partyID})
	if err != nil {
		log.Fatal("unable to write annonce", err)
	}

	// TODO: maybe add timeout
	msg, err := ws.ReadMessage()
	if err != nil {
		log.Fatal("error trying to read message")
	}

	m, ok := msg.(*proto.HostAnnonceResponse)
	if !ok || !m.Succeed {
		log.Fatal("failed to annonce itself")
	}

	// waiting for JoinRequest
	// fmt.Println("ID: ", partyID)

	msg, err = ws.ReadMessage()
	if err != nil {
		log.Fatal("error trying to read message")
	}

	gjr, ok := msg.(*proto.GuestJoinRequest)
	if !ok {
		log.Fatal("failed to annonce itself")
	}

	peer, err := NewPeerHost(tcps, udps)
	if err != nil {
		log.Fatal(err)
	}

	sdp, ices, err := peer.CreateOffer()
	if err != nil {
		log.Fatal("unable to create offer ", err)
	}

	err = ws.WriteMessage(&proto.GuestJoinOffer{
		Party: partyID,
		Guest: gjr.Guest,
		SDP:   sdp,
		ICES:  ices,
	})
	if err != nil {
		log.Fatal("unable to write guest offer", err)
	}

	msg, err = ws.ReadMessage()
	if err != nil {
		log.Fatal("error trying to read message")
	}

	gja, ok := msg.(*proto.GuestJoinAnswer)
	if !ok {
		log.Fatal("failed to annonce itself")
	}

	err = peer.AcceptAnswer(gja.SDP, gja.ICES)
	if err != nil {
		log.Fatal("unable to accept answer ", err)
	}

	err = ws.Close()
	if err != nil {
		log.Fatal("unable to close the ws", err)
	}

	select {}
}

type PeerHost struct {
	conn    *webrtc.PeerConnection
	ices    []*webrtc.ICECandidate
	icesMux sync.Mutex
	// tcpdc   *webrtc.DataChannel
}

func NewPeerHost(tcps []int, udps []int) (*PeerHost, error) {
	peerConn, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create a peer connection %w", err)
	}

	p := &PeerHost{
		conn: peerConn,
	}

	p.conn.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		p.icesMux.Lock()
		p.ices = append(p.ices, c)
		p.icesMux.Unlock()
	})

	p.conn.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		// fmt.Printf("Peer Connection State has changed: %s\n", pcs.String())
	})

	for _, tcp := range tcps {
		_, err := p.CreateTcpChannel(tcp)
		if err != nil {
			return nil, fmt.Errorf("unable to create make tcp channel %w", err)
		}
	}

	return p, nil
}

func (p *PeerHost) CreateOffer() (webrtc.SessionDescription, []*webrtc.ICECandidate, error) {
	offer, err := p.conn.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, nil, fmt.Errorf("unable to create an offer %w", err)
	}

	err = p.conn.SetLocalDescription(offer)
	if err != nil {
		return webrtc.SessionDescription{}, nil, fmt.Errorf("unable to set local desc %w", err)
	}

	<-webrtc.GatheringCompletePromise(p.conn)

	return offer, p.ices, nil
}

func (p *PeerHost) AcceptAnswer(sdp webrtc.SessionDescription, ices []*webrtc.ICECandidate) error {
	err := p.conn.SetRemoteDescription(sdp)
	if err != nil {
		return fmt.Errorf("unable to accept answer %w", err)
	}

	for _, ice := range ices {
		err := p.conn.AddICECandidate(ice.ToJSON())
		if err != nil {
			return fmt.Errorf("unable to add ice candidate %w", err)
		}
	}

	return nil
}

func (p *PeerHost) CreateTcpChannel(port int) (*webrtc.DataChannel, error) {
	fakeport := rand.Int63n(1000) + 9000

	ordered := true
	maxRetransmits := uint16(0)
	dc, err := p.conn.CreateDataChannel(fmt.Sprintf("tcp:%d", fakeport), &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create a dataChannel %w", err)
	}

	crecv := make(chan proto.RtcMessage)
	dialer := Dialer{}

	dc.OnOpen(func() {
		csend, err := dialer.Init(context.Background(), port, crecv)
		if err != nil {
			log.Fatal("unable to start the listener")
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
	})
	dc.OnError(func(err error) {
		log.Println("on channel error", err)
	})
	dc.OnClose(func() {
		log.Println("on channel close")
	})

	return dc, nil
}

type DialerState int

const (
	WAITING_FOR_CONN DialerState = iota
	TCP_CONN
)

type Dialer struct {
	port     int
	state    DialerState
	ctx      context.Context
	conn     net.Conn
	csend    chan proto.RtcMessage
	crecv    <-chan proto.RtcMessage
	fromRead <-chan NetMessage
}

func (d *Dialer) Init(ctx context.Context, port int, crecv <-chan proto.RtcMessage) (<-chan proto.RtcMessage, error) {
	d.ctx = ctx
	d.port = port
	d.crecv = crecv
	d.csend = make(chan proto.RtcMessage)

	d.state = WAITING_FOR_CONN
	go d.Serve()

	return d.csend, nil
}

func (d *Dialer) Serve() {
	for {
		select {
		case msg := <-d.fromRead:
			d.OnReadMessage(msg)

		case msg := <-d.crecv:
			d.OnRtcMessage(msg)
		}
	}
}

func (d *Dialer) OnReadMessage(msg NetMessage) {
	switch d.state {
	case TCP_CONN:

		switch msg.Type {
		case READ_MESSAGE:
			d.csend <- proto.RtcMessage{Type: proto.MESSAGE_TCP, Data: msg.Data}

		case READ_CLOSE:
			d.state = WAITING_FOR_CONN

			d.fromRead = nil
			d.conn = nil
			d.csend <- proto.RtcMessage{Type: proto.CLOSE_TCP_FROM_DIALER}

		default:
			log.Fatal("not impl")
		}

	default:
		log.Fatal("afsjfklqsfklqjfkl")
	}

}

func (d *Dialer) OnRtcMessage(msg proto.RtcMessage) {
	switch d.state {
	case WAITING_FOR_CONN:

		switch msg.Type {
		case proto.OPEN_TCP_REQUEST:
			c, err := net.Dial("tcp", fmt.Sprintf(":%d", d.port))
			if err != nil {
				d.csend <- proto.RtcMessage{Type: proto.OPEN_TCP_FAILED}
				return
			}

			d.conn = c
			d.fromRead = HandleRD(d.ctx, d.conn)
			d.state = TCP_CONN
			d.csend <- proto.RtcMessage{Type: proto.OPEN_TCP_SUCCESSFULL}

		default:
			log.Fatal("bbb")
		}

	case TCP_CONN:

		switch msg.Type {
		case proto.MESSAGE_TCP:
			_, err := d.conn.Write(msg.Data)
			if err != nil {
				log.Fatal("unable to forward message to dialer")
			}

		case proto.CLOSE_TCP_FROM_LISTENER:
			d.state = WAITING_FOR_CONN

			d.conn.Close()
			<-d.fromRead

			d.conn = nil
			d.fromRead = nil

		default:
			log.Fatal("errrazad")
		}

	default:
		log.Fatal("aaa")
	}
}

func HandleRD(ctx context.Context, conn net.Conn) <-chan NetMessage {
	cres := make(chan NetMessage)
	buff := [2048]byte{}

	go func() {
		defer close(cres)

		for {
			n, err := conn.Read(buff[:])
			if err != nil {
				cres <- NetMessage{Type: READ_CLOSE, Error: err}
				return
			}

			copy := slices.Clone(buff[:n])
			cres <- NetMessage{Type: READ_MESSAGE, Data: copy}
		}
	}()

	return cres
}
