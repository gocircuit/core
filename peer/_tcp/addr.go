// Copyright 2015 The Go Circuit Project
// Use of this source code is governed by the license for
// The Go Circuit Project, found in the LICENSE file.
//
// Authors:
//   2015 Petar Maymounkov <p@gocircuit.org>

package tcp

import (
	"github.com/gocircuit/alef/peer"
	"net"
)

// Addr implements peer.Addr
type Addr net.TCPAddr

func NewAddr(u *net.TCPAddr) peer.Addr {
	return (*Addr)(u)
}

func (a *Addr) TCP() *net.TCPAddr {
	return (*net.TCPAddr)(a)
}

func (a *Addr) String() string {
	return a.TCP().String()
}

func (a *Addr) Id() peer.Id {
	return peer.Id(a.String())
}
