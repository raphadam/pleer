package main

import (
	"log"
	"net/http"
	"pleer/proto"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	e := echo.New()
	s := Signal{
		parties: make(map[proto.ID]*Party),
	}

	e.GET("/", func(c echo.Context) error {
		conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			log.Fatal("unable to upgrade conn", err)
		}

		s.HandleConn(proto.WrapConn(conn))
		return c.String(http.StatusOK, "close conn normally")
	})

	e.Logger.Fatal(e.Start(":1323"))
}

type Signal struct {
	parties    map[proto.ID]*Party
	partiesMux sync.RWMutex
}

type Party struct {
	host   *proto.Conn
	guests []Guest
}

type Guest struct {
	id   proto.ID
	conn *proto.Conn
}

func (s *Signal) HandleConn(conn *proto.Conn) {
	defer conn.Close()

	for {
		msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			return
		}

		switch msg := msg.(type) {
		case *proto.HostAnnonceRequest:
			s.OnHostAnnonceRequest(conn, msg)

		case *proto.GuestJoinRequest:
			s.OnGuestJoinRequest(conn, msg)

		case *proto.GuestJoinOffer:
			s.OnGuestJoinOffer(conn, msg)

		case *proto.GuestJoinAnswer:
			s.OnGuestJoinAnswer(conn, msg)

		default:
			log.Fatal("not impl method")
		}
	}
}

func (s *Signal) OnHostAnnonceRequest(host *proto.Conn, msg *proto.HostAnnonceRequest) {
	party := &Party{
		host: host,
	}

	s.partiesMux.Lock()
	s.parties[msg.Party] = party
	s.partiesMux.Unlock()

	err := host.WriteMessage(&proto.HostAnnonceResponse{
		Succeed: true,
	})
	if err != nil {
		log.Fatal("unable to replay to annonce", err)
	}
}

func (s *Signal) OnGuestJoinRequest(guest *proto.Conn, msg *proto.GuestJoinRequest) {
	log.Println("we received the guest join request")

	g := Guest{
		id:   msg.Guest,
		conn: guest,
	}

	s.partiesMux.Lock()
	party, ok := s.parties[msg.Party]
	if !ok {
		log.Fatal("unable to find party")
	}
	party.guests = append(party.guests, g)
	s.partiesMux.Unlock()

	err := party.host.WriteMessage(msg)
	if err != nil {
		log.Fatal("unable to replay to annonce", err)
	}
}

func (s *Signal) OnGuestJoinOffer(host *proto.Conn, msg *proto.GuestJoinOffer) {
	var guest Guest

	s.partiesMux.RLock()
	party, ok := s.parties[msg.Party]
	if !ok {
		log.Fatal("unable to find party")
	}

	for _, g := range party.guests {
		if g.id == msg.Guest {
			guest = g
			break
		}
	}
	s.partiesMux.RUnlock()

	if guest.conn == nil {
		log.Fatal("unable to find guest")
	}

	err := guest.conn.WriteMessage(msg)
	if err != nil {
		log.Fatal("unable to replay to annonce", err)
	}
}

func (s *Signal) OnGuestJoinAnswer(guest *proto.Conn, msg *proto.GuestJoinAnswer) {
	s.partiesMux.RLock()
	party, ok := s.parties[msg.Party]
	s.partiesMux.RUnlock()

	if !ok {
		log.Fatal("unable to find party")
	}

	err := party.host.WriteMessage(msg)
	if err != nil {
		log.Fatal("unable to replay to annonce", err)
	}
}
