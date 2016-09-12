package webpipe

import (
	"bytes"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"time"
)

var upgrader = websocket.Upgrader{} // use default options

const HANDSHAKE_TIMEOUT = 2000 * time.Millisecond

var handshakeKey = []byte("WEBPIPE1")

type JSONObject map[string]interface {}

type WebPipe struct {
	pipe        *websocket.Conn
	subscribers map[string]map[string]chan Message
}

type Message struct {
	pipe            *WebPipe
	Name            string
	Args            []interface{}

	originalMessage JSONObject
	requestId       string
}
func (p *WebPipe) newMessage(msg JSONObject) *Message {
	//log.Printf("WebPipe: new message: %s\n", msg)

	m := new(Message)
	m.Name = msg["n"].(string)
	m.Args = msg["args"].([]interface{})

	m.pipe = p
	m.originalMessage = msg
	m.requestId = msg["requestId"].(string)

	return m
}

func (m Message) Reply(args... interface{}) error {
	if len(m.requestId) == 0 {  // message has no reply handler / is not expecting a reply
		return errors.New("message is not expecting a reply")
	}

	reply := JSONObject{
		"replyTo": m.requestId,
		"n": ("__webpipe_reply:" + m.Name),
		"args": args,
	}

	//log.Printf("WebPipe: sending '%s' reply\n", m.Name)
	//log.Printf("            args: %s\n", reply)

	return m.pipe.pipe.WriteJSON(reply)
}


func (p *WebPipe) On(event string, subscriberName string) chan Message {
	ch := make(chan Message, 1)

	_, exists := p.subscribers[event]
	if ! exists {
		log.Printf("subscription to new event: %s\n", event)
		p.subscribers[event] = make(map[string]chan Message)
	}

	p.subscribers[event][subscriberName] = ch

	return ch
}

func (p *WebPipe) Off(event string, subscriberName string) {
	delete(p.subscribers[event], subscriberName)
}

func (p *WebPipe) Emit(event string, args ...interface{}) {
	p.pipe.WriteJSON(JSONObject{
		"n":    event,
		"args": args,
	})
}

func New(w http.ResponseWriter, r *http.Request) (*WebPipe, error) {
	conn, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Print("websocket ERROR:", err)
		return nil, errors.New("webpipe: Not a websocket connection")
	}

	pipe := &WebPipe{
		pipe: conn,
		subscribers: make(map[string]map[string]chan Message),
	}

	if shakeHands(pipe) {
		return pipe, nil
	} else {
		pipe.pipe.Close()
		return nil, errors.New("webpipe: handshake failed!")
	}
}

func (p *WebPipe) Start() {
	go messageLoop(p)
}

func (p *WebPipe) dispatchMessage(msg *Message) {
	subs := p.subscribers[msg.Name]

	for _, ch := range subs {
		ch <-*msg
	}
}

func messageLoop(p *WebPipe) {
	log.Print("WebPipe: message loop started")

	defer p.pipe.Close()

	for {
		var message interface{}

		err := p.pipe.ReadJSON(&message)
		if err != nil {
			log.Println("WebPipe: read ERROR:", err)
			break
		}

		json, ok := message.(map[string]interface {}) // why doesn't message.(JSONObject) work?
		if !ok {
			log.Printf("WebPipe: Message not JSON: %s", message)
			break
		}

		msg := p.newMessage(json)

		p.dispatchMessage(msg)
	}

	log.Print("WebPipe: connection closed")
}

func shakeHands(p *WebPipe) bool {

	p.pipe.WriteMessage(websocket.TextMessage, handshakeKey)
	log.Print("WebPipe: sent intergalactic greeting phrase!")

	handsShaken := make(chan bool, 1)

	go func() {
		log.Print("WebPipe: waiting for client handshake...")
		mtype, handShake, err := p.pipe.ReadMessage()

		if err == nil && mtype == websocket.TextMessage && bytes.Compare(handShake, handshakeKey) == 0 {
			log.Print("WebPipe: hands successfully shaken!")
			handsShaken <- true
		} else {
			log.Printf("WebPipe: unknown handshake: %s", handShake)
		}
	}()

	// handshake or timeout, which happens first?
	select {
	case <-handsShaken: // hand shake completed
		return true

	case <-time.After(HANDSHAKE_TIMEOUT): // the hand shake timed out
		log.Printf("WebPipe: received no handshake! (within %.1fs)", HANDSHAKE_TIMEOUT.Seconds())
		p.pipe.WriteMessage(websocket.TextMessage, []byte("ERROR"))
		return false
	}
}
