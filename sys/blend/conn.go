// Copyright 2015 The Go Circuit Project
// Use of this source code is governed by the license for
// The Go Circuit Project, found in the LICENSE file.
//
// Authors:
//   2015 Petar Maymounkov <p@gocircuit.org>

package blend

import (
	"errors"
	"github.com/gocircuit/alef/sys"
	"github.com/gocircuit/alef/sys/tele/codec"
	"io"
	"net"
	"sync"
	"time"
)

var ErrClash = errors.New("clash")
var ErrGone = errors.New("gone")

// Conn implements sys.Conn.
type Conn struct {
	scrub func()
	user  chan *pipe // Send newly received pipes to user (via Read method)
	x     struct {   // Index of known open pipes
		lk   sync.Mutex
		n    PipeId
		open map[PipeId]*pipe
		use  time.Time
	}
	r sys.Conn // underlying connection for reading
	w struct {
		lk sync.Mutex // Linearize write ops on sub
		u  sys.Conn
	}
}

func (s *conn) init(user chan *pipe, under sys.Conn, scrub func()) {
	s.scrub = scrub
	s.user = user
	s.r, s.w.u = under, under
	s.x.open = make(map[PipeId]*pipe)
	s.x.use = time.Now()

	go s.readloop()
}

// Stat returns the number of pipes created on this end of the connection that
// have not been closed yet, as well as the last time the connection was used.
func (s *conn) Stat() (npipe int, lastuse time.Time) {
	s.x.lk.Lock()
	defer s.x.lk.Unlock()
	return len(s.x.open), s.x.use
}

// Addr implements sys.Conn.Addr.
func (s *conn) Addr() sys.Addr {
	return s.r.Addr()
}

func (s *conn) hijack() (u sys.Conn) {
	s.w.lk.Lock()
	defer s.w.lk.Unlock()
	u, s.w.u = s.w.u, nil
	return
}

func (s *conn) Close() (err error) {
	u := s.hijack()
	if u == nil {
		return io.ErrClosedPipe
	}
	// The closure of u is sensed in the readloop, which in turn
	// triggers the teardown sequence for this connection (notifying
	// all outstanding pipes that they have been broken).
	return u.Close()
}

func (s *conn) teardown() {
	// Notify Read (the reading user) that the connection is broken.
	if s.user != nil {
		close(s.user)
	}

	s.x.lk.Lock()
	// The substrate connection does not allow Write after Close.
	// To prevent writes from Conns hitting the substrate before the Conns have been notified:
	// we first remove the substrate from its field to prevents writes from Conn going through to it,
	// and then we close the substrate.
	if u := s.hijack(); u != nil {
		u.Close()
	}
	// Notify open pipes that they are now broken
	for id, p := range s.x.open {
		p.userClose()
		delete(s.x.open, id)
	}
	s.x.lk.Unlock()

	if s.scrub != nil {
		s.scrub()
	}
}

func (s *conn) readloop() {
	defer s.teardown()
	for {
		if err := s.read(); err != nil {
			return
		}
	}
}

func (s *conn) read() error {
	msg := &Msg{}
	if err := s.r.Read(msg); err != nil {
		return err
	}

	switch t := msg.Msg.(type) {
	case *PayloadMsg:
		p := s.get(msg.PipeId)
		if p != nil {
			// Existing pipe
			p.userWrite(t.Payload, nil)
			return nil
		}
		// Dead pipe
		if t.SeqNo > 0 {
			s.writeAbort(msg.PipeId, ErrGone)
			return nil
		}
		// New pipe
		p = newPipe(msg.PipeId, s)
		s.set(msg.PipeId, p)
		p.userWrite(t.Payload, nil)
		s.user <- p // Send new pipe to user
		return nil

	case *AbortMsg:
		p := s.get(msg.PipeId)
		if p == nil {
			// Discard closures for non-existent pipes
			// Do not respond with an abort message. This would cause an avalanche of abort messages.
			return nil
		}
		s.scrub(msg.PipeId)
		p.userWrite(nil, t.Reason)
		return nil
	}

	// Unexpected remote behavior
	return ErrClash
}

func (s *conn) count() int {
	s.x.lk.Lock()
	defer s.x.lk.Unlock()
	return len(s.x.open)
}

func (s *conn) get(id PipeId) *pipe {
	s.x.lk.Lock()
	defer s.x.lk.Unlock()
	s.x.use = time.Now()
	return s.x.open[id]
}

func (s *conn) set(id PipeId, p *pipe) {
	s.x.lk.Lock()
	defer s.x.lk.Unlock()
	s.x.open[id] = p
}

func (s *conn) scrub(id PipeId) {
	s.x.lk.Lock()
	defer s.x.lk.Unlock()
	delete(s.x.open, id)
}

func (s *conn) write(msg *Msg) error {
	s.w.lk.Lock()
	defer s.w.lk.Unlock()
	if s.w.u == nil {
		return io.ErrUnexpectedEOF
	}
	return s.w.u.Write(msg)
}

func (s *conn) writeAbort(id PipeId, reason error) error {
	msg := &Msg{
		PipeId: id,
		Msg: &AbortMsg{
			Reason: reason,
		},
	}
	return s.write(msg)
}
