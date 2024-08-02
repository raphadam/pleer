package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"strconv"
)

type ports []int

func (p *ports) String() string {
	return fmt.Sprintf("%v", *p)
}

func (p *ports) Set(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil {
		return errors.New("must provide a valid port number")
	}

	if port <= 0 || port > 65535 {
		return errors.New("port number not in range")
	}

	*p = append(*p, port)
	return nil
}

func main() {
	tcps := ports{}
	udps := ports{}

	join := flag.String("join", "", "join a host to  access ports")
	host := flag.Bool("host", false, "host the ports to forward")

	flag.Var(&tcps, "tcp", "define tcp port to forward")
	flag.Var(&udps, "udp", "define udp port to forward")

	flag.Parse()

	if *host {
		if len(tcps) <= 0 && len(udps) <= 0 {
			log.Fatal("should provide at least one port to forward")
		}

		Host(tcps, udps)
		return
	}

	if *join != "" {
		Join(*join)
		return
	}

	fmt.Println("must provide at least join or host")
}
