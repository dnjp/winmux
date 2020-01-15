/*

   A version of win written in Go. That does terminal multiplexing.

*/

package main

import (
	"9fans.net/go/acme"
	"bytes"
	"fmt"
	"github.com/rjkroege/winmux/ttypair"
	"log"
	//	"os"
	"sync"
	"unicode/utf8"
	//	"code.google.com/p/goplan9/draw"
	//	"image"
	"flag"
	"github.com/kr/pty"
	"os/exec"
	//	"os"
	"io"
)

type Q struct {
	// p int
	Win *acme.Win
	sync.Mutex
	Tty *ttypair.Tty
}

func main() {

	// take a window id from the command line
	// I suppose it could come from the environment too

	var q Q
	var err error

	// Actually make this correspond to the real syntax.
	var window_id = flag.Int("id", -1, "Acme window id")
	//var window_name = flag.String("name", "", "Acme window name")
	flag.Parse()

	cmd := "/usr/local/bin/rc"
	// cmd := "/usr/local/plan9/bin/cat"	// Safer for now.
	if args := flag.Args(); len(args) == 1 {
		cmd = args[0]
	} else if len(args) > 1 {
		log.Fatal(usage)
	}

	// TODO(rjkroege): look up a window by name if an argument is provided
	// and connect to it.
	// Hunt down an acme window
	if *window_id == -1 {
		q.Win, err = acme.New()
	} /* else open the named window. */
	if err != nil {
		log.Fatal("can't open the window? ", err.Error())
	}

	q.Win.Name("winmux-%s", cmd)
	q.Win.Fprintf("body", "hi rob\n")

	// TODO(rjkroege): start the function that receives from the pty and inserts into acme
	c := exec.Command(cmd)
	f, err := pty.Start(c)
	if err != nil {
		log.Fatalf("failed to start pty up: %s", err.Error())
	}

	echo := ttypair.Makecho()
	q.Tty = ttypair.New(f, echo)

	// A goroutine to read the output
	go childtoacme(&q, f, echo)

	// Read from the acme and send to child. (Rename?)
	acmetowin(&q, f, echo)

	q.Win.CloseFiles()
	fmt.Print("bye\n")
}

// Equivalent to the original implementation's sende function.
// Given the event, fetches the event's associated string (embeded, or by read)
// from the Acme, inserts the text into the Acme, adds the text into the winslice
// buffer and sends the typing to the client.
func sende(q *Q, t *ttypair.Tty, e *acme.Event, donl bool) {
	_, end := t.Extent()
	var err error
	var lastc rune

	if e.Nr > 0 {
		err = q.Win.Addr("#%d", end)
		_, err = q.Win.Write("data", e.Text)
		if err != nil {
			goto Error
		}
		lastc = rune(e.Text[len(e.Text)-1])
		t.Addtyping(e.Text, end)
	} else {
		// TODO(rjkroege): Write a generic helper to pull text out of Plan9
		buf := make([]byte, 128)
		nr := 0
		for m := e.Q0; m < e.Q1; m += nr {
			err = q.Win.Addr("#%d", m)
			nb, err := q.Win.Read("data", buf)
			nr = utf8.RuneCount(buf)
			err = q.Win.Addr("#%d", end)
			_, err = q.Win.Write("data", buf)
			if err != nil {
				goto Error
			}
			lastc = rune(buf[nb-1])
			t.Addtyping(buf, end)
			m += nr
			nr += nr
		}
	}

	if donl && lastc != '\n' {
		err = q.Win.Fprintf("data", "\n")
		if err != nil {
			goto Error
		}
		_, end = t.Extent()
		// TODO(rjkroege): consider doing something smarter about appending.
		// In particular: Addtyping could have a sibling Appendtyping method.
		t.Addtyping([]byte{'\n'}, end)
	}

	err = q.Win.Fprintf("ctl", "dot=addr")
	if err != nil {
		goto Error
	}
	t.Sendtype()
	return

Error:
	// TODO(rjkroege): Do something structured here. Acme may have gone away.
	// In general, error handling needs to be made robust.
	// Note that I currently believe that it is safe to only bother with looking at
	//
	log.Fatal("write error: \n", err.Error())
}

func unknown(e *acme.Event) {
	log.Printf("unknown message %c%c\n", e.C1, e.C2)
}

// Replicates the functionality of the stdinproc in win.c
// Reads the event stream from acme, updates the window and
// echos the received content.
func acmetowin(q *Q, f io.Writer, e *ttypair.Echo) {
	debug := false

	// TODO(rjkroege): This needs to be
	// this needs to be adjustable as I change buffers. could destroy/reconnect?
	// Some refactoring needed...
	t := q.Tty

	// TODO(rjkroege): extract the initial value of Offset from the Acme buffer.
	// TODO(rjkroege): verify the correctness of this position.
	t.Move(len("hi rob\n"))

	for {
		if debug {
			a, b := t.Extent()
			log.Printf("==> typing[%d,%d), %s\n", a, b, t)
		}
		e, err := q.Win.ReadEvent()
		if err != nil {
			log.Fatal("event stream stopped? ", err.Error())
		}
		if debug {
			log.Printf("msg %c%c q[%d,%d)... ", e.C1, e.C2, e.Q0, e.Q1)
		}

		// queue for lock
		q.Lock()

		switch e.C1 {
		default: // be backwards compatible: ignore additional future messages.
			unknown(e)
		case 'E': /* write to body or tag; can't affect us */
			switch e.C2 {
			case 'I', 'D': /* body */
				if debug {
					log.Printf("shift typing %d... ", e.Q1-e.Q0)
				}
				t.Move(e.Q1 - e.Q0)
			case 'i', 'd': /* tag */
			default:
				unknown(e)
			}
			break

		case 'F': /* generated by our actions; ignore */
		case 'K', 'M': // Keyboard or Mouse actions that edit the file
			switch e.C2 {
			case 'I': // text inserted into the body (This is a capital i)
				switch {
				case e.Nr == 1 && e.Text[0] == 0x7F:
					// handle delete characters: delete the character.
					// write addr, delete character
					//					char buf[1];
					//					fsprint(addrfd, "#%ud,#%ud", e.Q0, e.Q1);
					//					fswrite(datafd, "", 0);
					//					buf[0] = 0x7F;
					// ship DEL off to child.
					f.Write([]byte{0x7F})
				case t.Beforeslice(e.Q0):
					// Inserting before the final line. Doesn't affect the last line.
					if debug {
						log.Printf("shift typing %d... ", e.Q1-e.Q0)
					}
					t.Move(e.Q1 - e.Q0)
				case t.Inslice(e.Q0):
					// Typing in the final line.
					if debug {
						log.Printf("typing in last line")
					}
					t.Type(e /* afd, dfd */)
				}
			case 'D': // deleting text from the body
				n := t.Delete(e.Q0, e.Q1)
				// TODO(rjkroege): Delete from the winslice should
				// automatically update the Offset in Winslice?
				t.Move(-n)
				if t.Israw() && t.Afterslice(e.Q1, n) {
					t.Sendbs(n)
				}
				break
			case 'x', 'X': // button 2 in the tag or body
				// TODO(rjkroege): Copy the text to the bottom.
				if e.Flag&1 != 0 || (e.C2 == 'x' && e.Nr == 0) {
					/* send it straight back */
					q.Win.WriteEvent(e)
					break
				}
				if bytes.Equal([]byte("cook"), e.Text) {
					t.Setcook(true)
					break
				}
				if bytes.Equal([]byte("nocook"), e.Text) {
					t.Setcook(false)
					break
				}
				// original e.Flag & 8 case has e3 -> e.Arg, e4 -> e.Loc
				if e.Flag&8 > 0 {
					if e.Q1 != e.Q0 {
						// log.Printf("foo1")
						// func sende(q *Q, t *ttypair.Tty, e *acme.Event, donl bool) {
						// sende(q, t, e, false);

						// TODO(rjkroege): I don't understand.
						// sende(q, t, &blank,false);
					}
					// Already in e.Arg but in a different field.
					// sende(q,t, &e3, true);
				} else {
					// send something...
					// log.Printf("foo2")
					sende(q, t, e, true)
				}
			case 'l', 'L': // button 3, tag or body
				/* just send it back */
				q.Win.WriteEvent(e)
				break
			case 'd', 'i': // text deleted or inserted into the tag.
				break
			default:
				unknown(e)
			}
		}
		// Release the lock.
		q.Unlock()
	}
}