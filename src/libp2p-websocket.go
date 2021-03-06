/* Copyright (c) 2020, William R. Burdick Jr., Roy Riggs, and TEAM CTHLUHU
 *
 * The MIT License (MIT)
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in
 * all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
 * THE SOFTWARE.
 *
 */

package main

import (
	"context"
	"encoding/ascii85"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	//"encoding/binary"
	"bytes"
	"io/ioutil"
	"strconv"

	//"github.com/coreos/etcd/error"

	"github.com/go-errors/errors"
	ipfslite "github.com/hsanjuan/ipfs-lite"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	ipfsconfig "github.com/ipfs/go-ipfs-config"
	pinner "github.com/ipfs/go-ipfs-pinner"
	"github.com/ipfs/go-ipfs/namesys"
	ipfspath "github.com/ipfs/go-path"
	"github.com/libp2p/go-libp2p"
	autonat "github.com/libp2p/go-libp2p-autonat"
	connmgr "github.com/libp2p/go-libp2p-connmgr"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	discovery "github.com/libp2p/go-libp2p-discovery"
	dualdht "github.com/libp2p/go-libp2p-kad-dht/dual"
	protocol "github.com/libp2p/go-libp2p-protocol"
	libp2pquic "github.com/libp2p/go-libp2p-quic-transport"
	secio "github.com/libp2p/go-libp2p-secio"
	libp2ptls "github.com/libp2p/go-libp2p-tls"
	nat "github.com/libp2p/go-nat"

	//pubsub "github.com/libp2p/go-libp2p-pubsub"
	//pb "github.com/libp2p/go-libp2p-pubsub/pb"

	ma "github.com/multiformats/go-multiaddr"
	//logging "github.com/whyrusleeping/go-logging"

	goLog "github.com/ipfs/go-log"
	log2 "github.com/ipfs/go-log/v2"

	//"github.com/mr-tron/base58/base58"
	//autonatSvc "github.com/libp2p/go-libp2p-autonat-svc"

	"github.com/pkg/browser"
	treerequest "github.com/zot/textcraft-treerequest"
)

/*
 * Parts of this were taken from Abhishek Upperwal and Mantas Vidutis' libp2p chat example,
 * https://github.com/libp2p/go-libp2p-examples/tree/master/chat-with-rendezvous
 * and are Copyright (c) 2018 Protocol Labs, also licensed with the MIT license
 *
 * Some of these parts still survive in the code :)
 */

type addrList []ma.Multiaddr

type fileList []string

type retryError string

type libp2pRelay struct {
	relay
	host            host.Host
	discovery       *discovery.RoutingDiscovery
	natStatus       network.Reachability
	natActions      []func()                      // defer these until nat status known
	connectedPeers  map[peer.ID]*libp2pConnection // connected peers
	externalAddress string
}

type libp2pClient struct {
	client
	listeners           map[string]*listener         // protocol -> listener
	listenerConnections map[uint64]*listener         // connectionID -> listener
	forwarders          map[uint64]*libp2pConnection // connectionID -> forwarder
}

type libp2pConnection struct {
	connection
	peerID peer.ID
}

type listener struct {
	client         *libp2pClient                // the client that owns this listener
	connections    map[uint64]*libp2pConnection // connectionID -> connection
	protocol       string
	frames         bool        // whether to transmit frame lengths
	managementChan chan func() // client management
	closed         bool
}

const (
	portMapLeaseTime = 10 * time.Second // must be larger than 5 seconds
)

var clearTree = false
var publishTree cid.Cid
var hasNat = true
var customNatTraversal = true
var publicAddress atomic.Value // ma.Multiaddr
var test = ""
var decodeHash = ""
var p2pPort = 0
var useIPFSLite = true
var configDir = ""
var singleConnectionOpt = ""
var singleConnection = singleConnectionOpt == "true"
var versionCheckURL = ""
var versionID = ""
var curVersionID = ""
var defaultPage = "index.html"
var urlPrefix = "" // must begin and end with a slash or must be empty!
var centralRelay *libp2pRelay
var started bool
var logger = goLog.Logger("p2pmud")
var peerKeyString string
var listenAddresses addrList
var fakeNatStatus string
var bootstrapPeers addrList
var bootstrapPeerStrings = []string{
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
	"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
	"/ip4/104.236.179.241/tcp/4001/p2p/QmSoLPppuBtQSGwKDZT2M73ULpjvfd3aZ6ha4oFGL1KrGM",
	"/ip4/128.199.219.111/tcp/4001/p2p/QmSoLSafTMBsPKadTEgaXctDQVcqN88CNLHXMkTNwMKPnu",
	"/ip4/104.236.76.40/tcp/4001/p2p/QmSoLV4Bbm51jM9C4gDYZQ9Cy3U6aXMJDAbzgu2fzaDs64",
	"/ip4/178.62.158.247/tcp/4001/p2p/QmSoLer265NRgSp2LA3dPaeykiS1J6DifTC88f5uVQKNAd",
	"/ip6/2604:a880:1:20::203:d001/tcp/4001/p2p/QmSoLPppuBtQSGwKDZT2M73ULpjvfd3aZ6ha4oFGL1KrGM",
	"/ip6/2400:6180:0:d0::151:6001/tcp/4001/p2p/QmSoLSafTMBsPKadTEgaXctDQVcqN88CNLHXMkTNwMKPnu",
	"/ip6/2604:a880:800:10::4a:5001/tcp/4001/p2p/QmSoLV4Bbm51jM9C4gDYZQ9Cy3U6aXMJDAbzgu2fzaDs64",
	"/ip6/2a03:b0c0:0:1010::23:1001/tcp/4001/p2p/QmSoLer265NRgSp2LA3dPaeykiS1J6DifTC88f5uVQKNAd",
	"/ip4/104.131.131.82/udp/4001/quic/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
}
var conf struct {
	lite      *ipfslite.Peer
	myHost    host.Host
	dstor     datastore.Batching
	dht       *dualdht.DHT
	publisher *namesys.IpnsPublisher
	peerKey   crypto.PrivKey
	friends   map[peer.ID]bool
	treeName  string
	pin       pinner.Pinner
}
var peerFinder interface {
	FindPeer(context.Context, peer.ID) (peer.AddrInfo, error)
}

var logCount = 1
var accessChan chan network.Reachability = make(chan network.Reachability)

func (err retryError) Error() string {
	if err == "" {return "Retry error"}
	return string(err)
}

func createLibp2pRelay() *libp2pRelay {
	r := new(libp2pRelay)
	_, ok := interface{}(r).(protocolHandler)
	if !ok {
		log.Fatal("libp2pRelay does not support protocolHandler interface!")
	}
	r.init(r)
	r.connectedPeers = make(map[peer.ID]*libp2pConnection)
	return r
}

func getLibp2pRelay(r *relay) *libp2pRelay {
	return r.handler.(*libp2pRelay)
}

func (r *libp2pRelay) libp2pClient(c *client) *libp2pClient {
	return getLibp2pClient(c)
}

func (r *libp2pRelay) whenNatKnown(f func()) {
	svc(r, func() {
		if r.natStatus != network.ReachabilityUnknown {
			f()
		} else {
			r.natActions = append(r.natActions, f)
		}
	})
}

func (r *libp2pRelay) CreateClient() *client {
	c := new(libp2pClient)
	c.client.init(&r.relay, c)
	c.listeners = make(map[string]*listener)
	c.listenerConnections = make(map[uint64]*listener)
	c.forwarders = make(map[uint64]*libp2pConnection)
	return &c.client
}

func (r *libp2pRelay) CleanupClosed(con *connection) {}

// CloseClient API METHOD
func (r *libp2pRelay) CloseClient(c *client) {
	con := c.control
	if con != nil {
		delete(r.clients, con)
	}
	getLibp2pClient(c).Close()
}

// LISTEN API METHOD
func (r *libp2pRelay) Listen(cl *client, prot string, frames bool) {
	c := r.libp2pClient(cl)
	lis := c.createListener(prot, frames)
	for _, currentProt := range r.host.Mux().Protocols() {
		if currentProt == prot {
			c.writeMsgpack(&smsgListenRefusedParams{prot, "already listening to " + prot})
			return
		}
	}
	fmt.Println("listen, protocol: ", prot, ", frames: ", frames)
	r.host.SetStreamHandler(protocol.ID(prot), func(stream network.Stream) {
		fmt.Println("GOT A CONNECTION")
		svc(c, func() {
			con := c.createConnection(c.newConnectionID(), prot, stream, frames)
			fmt.Printf("GOT DIRECT CONNECTION ON %s FROM %s\n", prot, stream.Conn().RemotePeer().Pretty())
			lis.connections[con.id] = con
			c.listenerConnections[con.id] = lis
			c.writeMsgpack(&smsgListenerConnectionParams{strconv.FormatUint(con.id, 10), stream.Conn().RemotePeer().Pretty(), prot})
			c.read(&con.connection)
		})
	})
	c.writeMsgpack(&smsgListeningParams{prot})
}

// STOP LISTENER API METHOD
func (r *libp2pRelay) Stop(c *client, protocol string, retainConnections bool) {
	lc := getLibp2pClient(c)
	listener := lc.listeners[protocol]
	if listener != nil {
		listener.close(retainConnections)
	}
}

func (r *libp2pRelay) Versions() (string, string) {
	return vDate(versionID), vDate(curVersionID)
}

func (r *libp2pRelay) Started() bool {
	return started
}

func (r *libp2pRelay) Start(treeProtocol string, treeName string, port uint16, pk string, friends []string) error {
	var err error
	peerKeyString = pk
	conf.friends = make(map[peer.ID]bool)
	conf.treeName = treeName
	for _, friend := range friends {
		friendPeer, err := peer.Decode(friend)
		if err != nil {return fmt.Errorf("error decoding peerID %s: %w", friend, err)}
		conf.friends[friendPeer] = true
	}
	fmt.Println("STARTING RELAY...")
	if port != 0 {
		p2pPort = int(port)
	} else {
		p2pPort = 4005
	}
	addrs, err := stringsToAddrs([]string{
		fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic", p2pPort),
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", p2pPort),
	})
	if err != nil {return err}
	listenAddresses = addrs
	initp2p(treeProtocol)
	fmt.Println("STARTED")
	return nil
}

func (r *libp2pRelay) PeerAccess() chan network.Reachability {
	return accessChan
}

func (r *libp2pRelay) StartClient(c *client, init func(public bool, hasNat bool)) {
	go func() {
		r.whenNatKnown(func() {
			var public bool

			switch r.natStatus {
			case network.ReachabilityUnknown:
				fmt.Println("!!! UNKNOWN")
				public = true
			case network.ReachabilityPublic:
				fmt.Println("!!! PUBLIC")
				public = true
			case network.ReachabilityPrivate:
				fmt.Println("!!! PRIVATE")
				public = false
			}
			init(public, hasNat)
		})
	}()
}

func (r *libp2pRelay) HasConnection(c *client, id uint64) bool {
	return getLibp2pClient(c).hasConnection(id)
}

// CLOSE STREAM API METHOD
func (r *libp2pRelay) Close(c *client, id uint64) {
	lis := getLibp2pClient(c).listenerConnections[id]
	if lis != nil {
		fmt.Printf("CLOSING HOST CONNECTION %d\n", id)
		lis.removeConnection(id, false)
	}
	fwd := getLibp2pClient(c).forwarders[id]
	if fwd != nil {
		fmt.Printf("CLOSING PEER CONNECTION %d\n", id)
		fwd.close(func() {
			delete(getLibp2pClient(c).forwarders, id)
		})
	}
}

// SEND DATA API METHOD
func (r *libp2pRelay) Data(c *client, id uint64, data []byte) {
	lc := getLibp2pClient(c)
	con := lc.forwarders[id]

	if con == nil {
		lis := lc.listenerConnections[id]
		if lis != nil {
			con = lis.connections[id]
		}
	}
	if con != nil {
		fmt.Println("@@@ WRITING DATA TO CONNECTION")
		con.writeData(&r.relay, data)
	} else {
		fmt.Println("@@@ WRITING DATA TO CONNECTION")
		c.writeMsgpack(&smsgConnectionClosedParams{strconv.FormatUint(id, 10), "unknown connection"})
	}
}

// CONNECT API METHOD
func (r *libp2pRelay) Connect(c *client, prot string, peerid string, frames bool, relay bool) {
	type addrs struct {
		PeerID string
		Addrs  []string // the addrs of the peer
	}
	//var encodedAddrs addrs
	encodedAddrs := new(addrs)
	var addrInfo peer.AddrInfo
	relayMsg := "out"

	if relay {
		relayMsg = ""
	}
	if strings.HasPrefix(peerid, "/addrs/") {
		enc := strings.TrimPrefix(peerid, "/addrs/")
		dst := make([]byte, len(enc))
		ndst, _, err := ascii85.Decode(dst, []byte(enc), true)
		fmt.Println("Decoded", ndst, "bytes, len(dst) =", len(dst))
		if err != nil {
			c.connectionRefused(fmt.Errorf("could not decode addrs: %s", peerid), peerid, prot)
			return
		}
		fmt.Println("Decoding", string(dst[:ndst]))
		err = json.Unmarshal(dst[:ndst], encodedAddrs)
		if err != nil {
			c.connectionRefused(fmt.Errorf("could not decode addrs: %s", string(dst[:ndst])), peerid, prot)
			return
		}
		peerid = encodedAddrs.PeerID
		fmt.Println("Peer ID:", peerid)
		fmt.Printf("Addrs: %#v\n", encodedAddrs)
		addrInfo.Addrs = make([]ma.Multiaddr, len(encodedAddrs.Addrs))
		for i, addr := range encodedAddrs.Addrs {
			ma, err := ma.NewMultiaddr(addr)
			if err != nil {
				c.connectionRefused(fmt.Errorf("could not decode peer addr: %s", addr), peerid, prot)
				return
			}
			addrInfo.Addrs[i] = ma
		}
	}
	pid, err := peer.Decode(peerid)
	if err != nil {
		c.connectionRefused(fmt.Errorf("Error parsing peer id %s: %s", peerid, err), peerid, prot)
		return
	}
	addrInfo.ID = pid
	if encodedAddrs.PeerID == "" {
		fmt.Printf("Attempting to connect peer %s\n", pid.Pretty())
		maddr, err := ma.NewMultiaddr("/p2p/" + peerid)
		if err != nil {
			c.connectionRefused(fmt.Errorf("could not parse multiaddr %s", "/p2p/"+peerid), pid.Pretty(), prot)
			return
		}
		addrInfo.ID = pid
		addrInfo.Addrs = []ma.Multiaddr{maddr}
	}
	err = r.host.Connect(context.Background(), addrInfo)
	if err != nil {
		c.connectionRefused(fmt.Errorf("could not connect to peer %s: %s", pid.Pretty(), err.Error()), pid.Pretty(), prot)
		return
	}
	fmt.Printf("Attempting to connect with protocol %v to peer %v with%s relay\n", prot, peerid, relayMsg)
	if !relay {

		stream, err := r.host.NewStream(context.Background(), pid, protocol.ID(prot))
		if err != nil {
			fmt.Println("COULDN'T OPEN STREAM,", err)
			c.connectionRefused(err, peerid, prot)
			return
		}
		fmt.Println("Connected")
		lc := r.libp2pClient(c)
		//c.newConnection(smsgPeerConnection, prot, stream.Conn().RemotePeer().Pretty(), func(conID uint64) *connection {
		c.newConnection(prot, stream.Conn().RemotePeer().Pretty(), func(conID uint64) *connection {
			con := lc.createConnection(conID, prot, stream, frames)
			lc.forwarders[conID] = con
			return &con.connection
		})
	}
}

// FRIENDS API METHOD
func (r *libp2pRelay) Friends(add []string, remove []string) error {
	var err error
	addPeerIDs := make([]peer.ID, len(add))
	removePeerIDs := make([]peer.ID, len(remove))
	for i, friend := range add {
		addPeerIDs[i], err = peer.Decode(friend)
		if err != nil {return err}
	}
	for i, friend := range remove {
		removePeerIDs[i], err = peer.Decode(friend)
		if err != nil {return err}
	}
	treerequest.ChangePeers(conf.treeName, addPeerIDs, removePeerIDs)
	return nil
}

func logLine(str string, items ...interface{}) {
	log.Output(2, fmt.Sprintf("[%d] %s", logCount, fmt.Sprintf(str, items...)))
	logCount++
}

func (r *libp2pRelay) printAddresses() {
	fmt.Println("Addresses:")
	printMaddrs(r.host.Addrs(), "/p2p/"+r.peerID)
}
func printMaddrs(addrs []ma.Multiaddr, suffix string) {
	for _, addr := range addrs {
		fmt.Println("   ", addr.String()+suffix)
	}
}

func (r *libp2pRelay) AddressArray() []string {
	output := make([]string, 0, len(r.host.Addrs()))
	for _, addr := range r.host.Addrs() {
		output = append(output, addr.String())
	}
	return output
}

func (r *libp2pRelay) AddressesJson() string {
	buf := bytes.NewBuffer(make([]byte, 0, 16))
	fmt.Println("Getting addresses...")
	buf.WriteByte(byte('['))
	first := true
	for _, addr := range r.host.Addrs() {
		if first {
			first = false
		} else {
			buf.WriteByte(byte(','))
		}
		buf.WriteByte(byte('"'))
		buf.Write([]byte(addr.String()))
		buf.WriteByte(byte('"'))
		fmt.Println("Address: " + addr.String())
	}
	buf.WriteByte(byte(']'))
	return buf.String()
}

func (r *libp2pRelay) PeerKey() string {
	return peerKeyString
}

func (r *libp2pRelay) setNATStatus(status network.Reachability) {
	r.natStatus = status
	for _, f := range r.natActions {
		f()
	}
	r.natActions = []func(){}
}

func createListener() *listener {
	lis := new(listener)
	lis.connections = make(map[uint64]*libp2pConnection)
	lis.managementChan = make(chan func())
	return lis
}

func (l *listener) close(retainConnections bool) {
	for id := range l.connections {
		l.removeConnection(id, retainConnections)
	}
	l.client.writeMsgpack(&smsgListenerClosedParams{l.protocol})
	l.closePrim()
}

func (l *listener) closePrim() {
	l.client.libp2pRelay().host.RemoveStreamHandler(protocol.ID(l.protocol))
	delete(l.client.listeners, l.protocol)
	for conID := range l.connections {
		delete(l.client.listenerConnections, conID)
	}
	l.closed = true
}

func (l *listener) removeConnection(id uint64, retainConnections bool) {
	if retainConnections {
		fmt.Println("RETAINING SERVICE CONNECTION ", id)
		svc(l.client, func() {
			l.client.forwarders[id] = l.connections[id]
			delete(l.client.listenerConnections, id)
		})
	} else {
		fmt.Println("CLOSING SERVICE CONNECTION ", id)
		l.connections[id].close(func() {
			svc(l.client, func() {
				delete(l.client.listenerConnections, id)
			})
		})
	}
	delete(l.connections, id)
}

func getLibp2pClient(c *client) *libp2pClient {
	return c.data.(*libp2pClient)
}

func (c *libp2pClient) libp2pRelay() *libp2pRelay {
	return getLibp2pRelay(c.relay)
}

func (c *libp2pClient) Close() {
	svc(c, func() {
		for _, l := range c.listeners {
			l.close(false)
		}
		for _, con := range c.forwarders {
			con.close(func() {})
		}
		if c.control != nil {
			c.control.Close()
			c.control = nil
		}
		c.client.close()
	})
}

func (c *libp2pClient) hasConnection(conID uint64) bool {
	return c.listenerConnections[conID] != nil || c.forwarders[conID] != nil
}

func (c *libp2pClient) createConnection(conID uint64, prot string, stream network.Stream, frames bool) *libp2pConnection {
	con := new(libp2pConnection)
	con.peerID = stream.Conn().RemotePeer()
	con.connection.init("connection", prot, conID, stream, &c.client, frames, con)
	fmt.Println("MAKING CONNECTION WITH ID ", con.id)
	getLibp2pRelay(c.relay).host.ConnManager().Protect(con.peerID, "websocket")
	svc(c.relay, func() { c.libp2pRelay().connectedPeers[con.peerID] = con })
	return con
}

func (c *libp2pClient) createListener(prot string, frames bool) *listener {
	lis := createListener()
	lis.frames = frames
	lis.protocol = prot
	c.listeners[prot] = lis
	lis.client = c
	return lis
}

func getLibp2pConnection(con *connection) *libp2pConnection {
	return con.data.(*libp2pConnection)
}

func checkErrWithMsg(err error, msg string) {
	if err != nil {
		fmt.Println(msg)
		e, ok := err.(*errors.Error)
		if ok {
			fmt.Println(e.ErrorStack())
		}
		panic(err)
	}
}

func checkErr(err error) {
	if err != nil {
		e, ok := err.(*errors.Error)
		if ok {
			fmt.Println(e.ErrorStack())
		}
		panic(err)
	}
}

func checkVersion() {
	if versionCheckURL != "" && centralRelay.peerID != "" {
		fmt.Println("This version:", versionID)
		seconds, nanos := versionNumbers(versionID)
		fmt.Println("FETCHING", fmt.Sprintf(versionCheckURL, centralRelay.peerID, seconds, nanos))
		resp, err := http.Get(fmt.Sprintf(versionCheckURL, centralRelay.peerID, seconds, nanos))
		if err != nil {
			fmt.Println("Error: ", err.Error())
		} else {
			defer resp.Body.Close()
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("Error: ", err.Error())
			} else {
				curVersionID = strings.TrimSpace(string(body))
				fmt.Println("This version :", vDate(versionID), "\nCurrent version: ", vDate(curVersionID))
			}
		}
	}
}

func initp2p(treeProtocol string) {
	var keyBytes []byte
	var err error
	var opts []libp2p.Option

	started = true
	goLog.SetAllLoggers(log2.LevelWarn)
	goLog.SetLogLevel("rendezvous", "info")
	ctx := context.Background()
	if useIPFSLite {
		if customNatTraversal {
			opts = []libp2p.Option{
				//libp2p.NATPortMap(),
				libp2p.ConnectionManager(connmgr.NewConnManager(50, 300, time.Minute)),
				libp2p.EnableAutoRelay(),
				libp2p.EnableNATService(),
				libp2p.Security(libp2ptls.ID, libp2ptls.New),
				libp2p.Security(secio.ID, secio.New),
				libp2p.DefaultTransports,
			}
		} else {
			opts = ipfslite.Libp2pOptionsExtra
		}
		if peerKeyString == "" {
			conf.peerKey, _, err = crypto.GenerateKeyPair(crypto.RSA, 2048)
			fmt.Println("   ###   ADDING PEER KEY")
		}
	} else {
		opts = []libp2p.Option{
			libp2p.ListenAddrs([]ma.Multiaddr(listenAddresses)...),
			libp2p.ConnectionManager(connmgr.NewConnManager(50, 300, time.Minute)),
			libp2p.EnableAutoRelay(),
			libp2p.EnableNATService(),
			libp2p.Security(libp2ptls.ID, libp2ptls.New),
			libp2p.Security(secio.ID, secio.New),
			libp2p.DefaultTransports,
		}
		if !customNatTraversal {
			opts = append(opts, libp2p.NATPortMap())
		}
	}
	opts = append(opts, libp2p.Transport(libp2pquic.NewTransport))
	if peerKeyString != "" { // add peer key into opts if provided
		keyBytes, err = crypto.ConfigDecodeKey(peerKeyString)
		checkErr(err)
		fmt.Printf("###\n### KEY BYTES: %d\n", len(keyBytes))
		conf.peerKey, err = crypto.UnmarshalPrivateKey(keyBytes)
		checkErr(err)
		fmt.Println("   ###   ADDING PEER KEY")
		if !useIPFSLite {
			opts = append(opts, libp2p.Identity(conf.peerKey))
		}
	}
	if fakeNatStatus == "public" {
		opts = append(opts, libp2p.ForceReachabilityPublic())
	} else if fakeNatStatus == "private" {
		opts = append(opts, libp2p.ForceReachabilityPrivate())
	}
	fmt.Printf("%+v\n", opts)
	fakeNatStatus = ""
	if customNatTraversal {
		mapping := <-mapPort(context.Background(), p2pPort)
		if mapping.err == nil {
			var addr ma.Multiaddr
			addrStr := mapping.addr.IP.String() + "/tcp/" + strconv.Itoa(mapping.addr.Port)

			fmt.Printf("IP:%v[%d]\n", mapping.addr.IP, len(strings.Split(mapping.addr.IP.String(), ".")))
			if len(strings.Split(mapping.addr.IP.String(), ".")) == 4 {
				addr, err = ma.NewMultiaddr("/ip4/" + addrStr)
			} else {
				addr, err = ma.NewMultiaddr("/ip6/" + addrStr)
			}
			if err == nil {
				publicAddress.Store(addr) // set initial public address
				opts = append(opts, libp2p.AddrsFactory(func(addrs []ma.Multiaddr) []ma.Multiaddr {
					proto := ma.P_IP4
					var addrStr string
					addr, _ := publicAddress.Load().(ma.Multiaddr)
					if addr != nil {
						addrStr, err = addr.ValueForProtocol(proto)
						if err != nil {
							proto = ma.P_IP6
							addrStr, err = addr.ValueForProtocol(proto)
						}
					}
					if addr == nil || err != nil {return addrs}
					// replace addr for same IP addr in case it changed
					for i, tmpAddr := range addrs {
						var tmpStr string
						tmpStr, err = tmpAddr.ValueForProtocol(proto)
						if err == nil && tmpStr == addrStr {
							addrs[i] = addr
							return addrs
						}
					}
					return append(addrs, addr)
				}))
			}
		}
	}
	if useIPFSLite {
		ipfsDir, err := ipfsconfig.Filename("")
		checkErr(err)
		path := filepath.Join(filepath.Dir(ipfsDir), configDir)
		fmt.Println("DATA STORE:", path)
		if _, err = os.Stat(path); err != nil {
			parent := filepath.Dir(path)
			_, err = os.Stat(parent)
			if err != nil {
				grandParent := filepath.Dir(parent)
				_, err = os.Stat(grandParent)
				if err != nil {
					fmt.Printf("\n\nCOULD NOT CREATE CONFIG DIRECTORY: %s\n\n", path)
					panic(err)
				}
				err = os.Mkdir(parent, 0700)
				if err != nil {
					fmt.Printf("\n\nCOULD NOT CREATE CONFIG DIRECTORY PARENT: %s\n\n", parent)
					panic(err)
				}
			}
			err = os.Mkdir(path, 0700)
			checkErr(err)
		}
		fmt.Println("Datastore:", path)
		conf.dstor, err = ipfslite.BadgerDatastore(path)
		checkErr(err)
		fmt.Println("Listen addresses:")
		printMaddrs(listenAddresses, "")
		conf.myHost, conf.dht, err = ipfslite.SetupLibp2p(
			ctx,
			conf.peerKey,
			nil,
			listenAddresses,
			conf.dstor,
			opts...,
		)
		checkErr(err)
		conf.lite, err = ipfslite.New(ctx, conf.dstor, conf.myHost, conf.dht, nil)
		checkErr(err)
		conf.publisher = namesys.NewIpnsPublisher(conf.dht, conf.dstor)
		conf.pin, err = pinner.LoadPinner(conf.dstor, conf.lite, conf.lite)
		if err != nil {
			conf.pin = pinner.NewPinner(conf.dstor, conf.lite, conf.lite)
		}
	} else {
		conf.myHost, err = libp2p.New(ctx, opts...)
	}
	checkErr(err)
	fmt.Println("Addrs:", conf.myHost.Addrs())
	centralRelay.peerID = conf.myHost.ID().Pretty()
	centralRelay.host = conf.myHost
	checkVersion()
	if fakeNatStatus == "public" {
		centralRelay.setNATStatus(network.ReachabilityPublic)
		err := initTree(ctx, treeProtocol)
		checkErr(err)
	} else if fakeNatStatus == "private" {
		centralRelay.setNATStatus(network.ReachabilityPrivate)
		err := initTree(ctx, treeProtocol)
		checkErr(err)
	} else {
		/// MONITOR NAT STATUS
		fmt.Println("Creating autonat")
		//ctx, cancel := context.WithCancel(context.Background())
		ctx := context.Background()
		an, err := autonat.New(ctx, conf.myHost)
		checkErr(err)
		centralRelay.natStatus = network.ReachabilityUnknown
		//need to check reachability even when not natted because of fw rules
		go func() {
			peeped := false
			timer := time.NewTimer(0)
			oldAddr, _ := publicAddress.Load().(ma.Multiaddr)
			init := true

			for running := true; running; {
				timer.Reset(1 * time.Second) // check autonat every second until it finds status
				_, ok := <-timer.C
				if !ok {break}
				status := an.Status()
				svcSync(centralRelay, func() interface{} {
					if status != centralRelay.natStatus || !peeped {
						fmt.Println("@@@ NAT status", natStatus(status))
						addr, err := an.PublicAddr()
						if err == nil {
							fmt.Println("@@@ PUBLIC ADDRESS: ", addr)
							centralRelay.printAddresses()
							if customNatTraversal && oldAddr != addr {
								publicAddress.Store(addr)
							}
						}
						if status != network.ReachabilityUnknown {
							centralRelay.setNATStatus(status)
						}
						accessChan <- status
						if init {
							init = false
							err := initTree(ctx, treeProtocol)
							checkErr(err)
						}
					}
					return nil
				})
				peeped = true
			}
		}()
	}
	conf.peerKey = conf.myHost.Peerstore().PrivKey(conf.myHost.ID())
	keyBytes, err = crypto.MarshalPrivateKey(conf.peerKey)
	checkErr(err)
	peerKeyString = crypto.ConfigEncodeKey(keyBytes)
	fmt.Printf("host private %s key: %s\n", reflect.TypeOf(conf.peerKey), peerKeyString)

	if useIPFSLite {
		conf.lite.Bootstrap(ipfslite.DefaultBootstrapPeers())
	} else {
		// Let's connect to the bootstrap nodes first. They will tell us about the
		// other nodes in the network.
		var wg sync.WaitGroup

		remaining := int32(len(bootstrapPeers))
		fmt.Printf("@@@ WAITING FOR %d bootstrap peer connections...\n", remaining)
		for _, peerAddr := range bootstrapPeers {
			peerinfo, err := peer.AddrInfoFromP2pAddr(peerAddr)
			if err != nil {continue}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := conf.myHost.Connect(ctx, *peerinfo); err != nil {
					logger.Warning(err)
				} else {
					logger.Info("Connection established with bootstrap node:", *peerinfo)
				}
				rem := atomic.AddInt32(&remaining, -1)
				fmt.Printf("@@@ WAITING FOR %d bootstrap peer connections...\n", rem)
			}()
		}
		wg.Wait()
	}
	centralRelay.printAddresses()
	fmt.Println("FINISHED INITIALIZING P2P, CREATING RELAY")
	runSvc(centralRelay)
	fmt.Printf("Peer id: %v\n", centralRelay.peerID)
	if decodeHash != "" && useIPFSLite {
		///// fetch the node, try using ipld.Decode(NewBlock(node.RawData())) to make a node
		location := "local"
		cid, err := cid.Decode(decodeHash)
		checkErr(err)
		fmt.Printf("CID: %v\n", cid)
		block, err := conf.lite.BlockStore().Get(cid)
		checkErr(err)
		if block == nil {
			location = "remote"
			block, err = conf.lite.Session(context.Background()).Get(context.Background(), cid)
			checkErr(err)
		}

		fmt.Printf("Block [%s]: %v\n", location, block)
	}

}

func initTree(ctx context.Context, treeProtocol string) error {
	if conf.lite == nil {return nil}
	publish := make(map[string]cid.Cid)
	if clearTree {
		publish = nil
		fmt.Println("###\n### REQUESTING TREE CLEAR\n###")
	} else if publishTree != cid.Undef {
		fmt.Println("###\n### SENDING TREE", publishTree, "TO PUBLISH AS", conf.treeName, "\n###")
		publish[conf.treeName] = publishTree
	} else {
		fmt.Println("###\n### NOT SENDING ANY TREE TO PUBLISH\n###")
	}
	checkErr(treerequest.InitTreeRequest(treeProtocol, conf.dstor, conf.myHost, conf.peerKey, conf.lite.BlockStore(), conf.lite.Session(ctx), conf.lite, conf.pin, 10000, publish, treerequestConnection))
	treerequest.ChangePeers(conf.treeName, friends(), nil)
	treerequest.HandlePeerFileRequests("/peerEncrypted/", true, publishFile)
	treerequest.HandlePeerFileRequests("/peer/", false, publishFile)
	tree, err := treerequest.GetTree(conf.treeName, conf.myHost.ID())
	if err != nil && err != datastore.ErrNotFound {return err}
	if err == nil { // publish to IPNS on startup
		publishFile("/", tree.Root())
	}
	return nil
}

func friends() []peer.ID {
	friends := make([]peer.ID, len(conf.friends))
	i := 0
	for friend := range conf.friends {
		friends[i] = friend
		i++
	}
	return friends
}

func treerequestConnection(peerID peer.ID) {
	fmt.Println("###\n### NEW CONNECTION:", peerID, "\n###")
}

func publishFile(filename string, newRoot cid.Cid) {
	go func() {
		err := conf.publisher.Publish(context.Background(), conf.peerKey, ipfspath.FromCid(newRoot))
		if err != nil {
			fmt.Printf("Error: %s\n", err)
		}
	}()
}

func natStatus(status network.Reachability) string {
	switch status {
	case network.ReachabilityUnknown:
		return "UNKNOWN"
	case network.ReachabilityPublic:
		return "PUBLIC"
	case network.ReachabilityPrivate:
		return "PRIVATE"
	default:
		return "BAD NETWORK STATUS"
	}
}

func versionNumbers(v string) (string, string) {
	fmt.Println("Checking version: ", v)
	times := strings.Split(v, ".")
	seconds := times[0]
	nanos := times[1]
	return seconds, strings.Repeat("0", len(nanos)-9) + nanos
}

func vDate(v string) string {
	if v == "" {return ""}
	secStr, nanoStr := versionNumbers(v)
	seconds, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		seconds = 0
	}
	nanos, err := strconv.ParseInt(nanoStr, 10, 64)
	if err != nil {
		nanos = 0
	}
	t := time.Unix(seconds, nanos)
	return t.Format("2006-01-02T03:04:05PM-07:00")
}

func (fl *fileList) String() string {
	strs := make([]string, len(*fl))
	for i, file := range *fl {
		strs[i] = file
	}
	return strings.Join(strs, ",")
}

func (fl *fileList) Set(value string) error {
	_, err := os.Stat(value)
	if err != nil {return err}
	*fl = append(*fl, value)
	return nil
}

func (al *addrList) String() string {
	strs := make([]string, len(*al))
	for i, addr := range *al {
		strs[i] = addr.String()
	}
	return strings.Join(strs, ",")
}

func (al *addrList) Set(value string) error {
	addr, err := ma.NewMultiaddr(value)
	if err != nil {return err}
	*al = append(*al, addr)
	return nil
}

func stringsToAddrs(addrStrings []string) (maddrs []ma.Multiaddr, err error) {
	for _, addrString := range addrStrings {
		addr, err := ma.NewMultiaddr(addrString)
		if err != nil {return maddrs, err}
		maddrs = append(maddrs, addr)
	}
	return

}

type portMapResult struct {
	natter nat.NAT
	addr   net.TCPAddr
	err    error
}

func isPrivateIPv4(addr net.IP) bool {
	a := addr[0]
	b := addr[1]
	c := addr[2]

	return a == 10 ||
		(a == 100 && b >= 64 && b <= 127) ||
		a == 127 ||
		(a == 172 && b >= 16 && b <= 31) ||
		(a == 169 && b == 254) ||
		(a == 192 && b == 0) ||
		(a == 192 && b == 2) ||
		(a == 192 && b == 88 && c == 99) ||
		(a == 192 && b == 168) ||
		(a == 198 && b >= 18 && b <= 19) ||
		(a == 198 && b == 51 && c == 100) ||
		(a == 203 && b == 0 && c == 113) ||
		a >= 224
}

func isPrivateIPv6(addr net.IP) bool {
	return (addr[15] == 1 && hasValues(addr, 0, 0, 15)) ||
		addr[0] == 100 ||
		addr[0] >= 0xFC ||
		(hasValues(addr, 0, 0, 11) &&
			hasValues(addr, 255, 11, 13) &&
			isPrivateIPv4(net.IP{addr[12], addr[13], addr[14], addr[15]}))
}

func hasValues(addr net.IP, value byte, start int, end int) bool {
	for i := start; i < end; i++ {
		if addr[i] != value {return false}
	}
	return true
}

func isPrivate(addr net.IP) bool {
	if len(addr) == 4 {return isPrivateIPv4(addr)}
	return isPrivateIPv6(addr)
}

func mapPort(ctx context.Context, port int) chan portMapResult {
	result := make(chan portMapResult)
	go func() {
		fmt.Println("DISCOVERING NAT CONTROLLERS...")
		natChan := nat.DiscoverNATs(context.Background())
		var natter nat.NAT
		timer := time.NewTimer(5 * time.Second)

		select {
		case <-timer.C:
			timer.Stop()
			hasNat = false
			fmt.Println("NO NAT MANAGER FOUND")
			result <- portMapErrString("No NAT found")
			return
		case natter = <-natChan:
			timer.Stop()
			fmt.Println("FOUND NAT MANAGER")
			ip, err := natter.GetExternalAddress()
			if err != nil {
				result <- portMapErr(err)
				return
			}
			fmt.Println("EXTERNAL ADDRESS:", ip)
			extPort, err := natter.AddPortMapping("tcp", port, "port for websocket peer", portMapLeaseTime)
			if err != nil {
				result <- portMapErr(err)
				return
			}
			fmt.Println("MAPPED PORT:", extPort)
			go func() {
			loop:
				for {
					timer.Reset(portMapLeaseTime - 5*time.Second)
					select {
					case <-ctx.Done():
						break loop
					case <-timer.C:
						_, err := natter.AddPortMapping("tcp", port, "port for websocket peer", portMapLeaseTime)
						if err != nil {
							break loop
						}
					}
				}
			}()
			result <- portMapResult{natter, net.TCPAddr{IP: ip, Port: extPort, Zone: ""}, nil}
		}
	}()
	return result
}

func portMapErrString(err string) portMapResult {
	return portMapResult{nil, net.TCPAddr{IP: net.IPv4zero, Port: 0, Zone: ""}, fmt.Errorf(err)}
}

func portMapErr(err error) portMapResult {
	return portMapResult{nil, net.TCPAddr{IP: net.IPv4zero, Port: 0, Zone: ""}, err}
}

func errstrw(format string, args ...interface{}) string {
	return errstr(format+": %w", args...)
}

func errstr(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...) + "\n" + strings.Join(strings.Split(string(debug.Stack()[2:]), "\n")[5:], "\n")
}

func httpError(w http.ResponseWriter, errMsg string, code int) error {
	http.Error(w, errMsg, code)
	return nil
}

func validateID(id string) error {
	_, err := peer.Decode(id)
	return err

}

func handleUrlEffect(service string, input func(input string) error) {
	http.HandleFunc(service, func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Handling %s (%s)\n", r.URL, r.URL.Path[len(service):])
		err := input(r.URL.Path[len(service):])
		if err != nil {
			httpError(w, errstr("Could not read request body"), http.StatusBadRequest)
			return
		}
		w.WriteHeader(200)
	})
}

func handlePeerCID(urlPath string) (interface{}, error) {
	pind := strings.Index(urlPath, "/")
	peerID, err := peer.Decode(urlPath[0:pind])
	if err != nil {return nil, fmt.Errorf("Bad peer id: %s", urlPath[0:pind])}
	nind := pind + 1 + strings.Index(urlPath[pind+1:], "/")
	if nind < pind+1 {
		nind = len(urlPath)
	}
	treeName := urlPath[pind+1 : nind]
	file := path.Clean("/" + urlPath[nind:])
	fmt.Println("Fetching tree")
	tree, _, err := treerequest.FetchSync(treeName, peerID, false)
	if err != nil {return nil, fmt.Errorf(err.Error())}
	fmt.Printf("Tree: %v\n", tree)
	aCid := tree.Nodes[file]
	if aCid == cid.Undef {return nil, fmt.Errorf("no tree for %v/%v", peerID, file)}
	return aCid.String(), nil
}

func handleUrlJSON(service string, input func(input string) (interface{}, error)) {
	http.HandleFunc(service, func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Handling %s (%s)\n", r.URL, r.URL.Path[len(service):])
		value, err := input(r.URL.Path[len(service):])
		if err != nil {
			httpError(w, errstr("Could not read request body"), http.StatusBadRequest)
			return
		}
		output, err := json.Marshal(value)
		if err != nil {
			httpError(w, errstr("Could not encode result %v", value), http.StatusBadRequest)
			return
		}
		http.ServeContent(w, r, "output.json", time.Now(), bytes.NewReader(output))
	})
}

func main() {
	started = false
	log.SetFlags(log.Lshortfile)
	browse := ""
	nobrowse := false
	centralRelay = createLibp2pRelay()
	addr := "localhost"
	port := 8888
	noBootstrap := false
	bootstrapArg := addrList([]ma.Multiaddr{})
	fileList := fileList([]string{})
	fakeNATPrivate := false
	fakeNATPublic := false
	version := false
	noIPFS := false
	publishTreeString := ""

	flag.StringVar(&decodeHash, "decode", "", "Test decodinf an IPFS block")
	flag.BoolVar(&noIPFS, "noipfs", false, "Don't use ipfs")
	flag.StringVar(&configDir, "config", configDir, "Name of the subdirectory within the ipfs config directory to use for the config")
	flag.BoolVar(&noBootstrap, "nopeers", false, "Clear the bootstrap peer list")
	flag.StringVar(&peerKeyString, "key", "", "Specify peer key")
	flag.Var(&fileList, "files", "Add the contents of a directory to serve from /")
	flag.StringVar(&addr, "addr", "", "Host address to listen on")
	flag.IntVar(&port, "port", port, "Port to listen on")
	flag.Var(&bootstrapArg, "peer", "Adds a peer multiaddress to the bootstrap list")
	flag.Var(&listenAddresses, "listen", "Adds a multiaddress to the listen list")
	flag.StringVar(&browse, "browse", defaultPage, "Launch browser with this page")
	flag.BoolVar(&nobrowse, "nobrowse", false, "Do not launch browser")
	flag.BoolVar(&fakeNATPrivate, "fakenatprivate", false, "Pretend nat is private")
	flag.BoolVar(&fakeNATPublic, "fakenatpublic", false, "Pretend nat is publc")
	flag.BoolVar(&version, "version", false, "Print version number and exit")
	roy, bill := false, false
	flag.BoolVar(&roy, "roy", false, "Test as Roy")
	flag.BoolVar(&bill, "bill", false, "Test as Bill")
	flag.StringVar(&publishTreeString, "tree", "", "IPFS tree to publish")
	flag.BoolVar(&clearTree, "cleartree", false, "Clear the published tree")
	if roy {
		test = "roy"
	} else if bill {
		test = "bill"
	}
	flag.Parse()
	if publishTreeString != "" {
		var err error
		publishTree, err = cid.Decode(publishTreeString)
		checkErr(err)
	}
	useIPFSLite = !noIPFS
	if useIPFSLite {
		fmt.Println("Using IPFS")
	} else {
		fmt.Println("Not using IPFS")
	}
	if version {
		fmt.Println("Version ", versionID)
		os.Exit(0)
	}
	bootstrapPeers, _ = stringsToAddrs(bootstrapPeerStrings)
	if len(bootstrapArg) > 0 {
		bootstrapPeers = append(bootstrapPeers, bootstrapArg...)
	}
	if fakeNATPrivate {
		fakeNatStatus = "private"
	} else if fakeNATPublic {
		fakeNatStatus = "public"
	}
	fmt.Printf("Listening on port %v\n", port)
	http.HandleFunc("/libp2p", centralRelay.handleConnection())
	handleUrlEffect("/peerID/", validateID)
	handleUrlJSON("/peerCID/", handlePeerCID)
	if len(fileList) > 0 {
		for _, dir := range fileList {
			fmt.Println("File dir: ", dir)
		}
		http.HandleFunc(urlPrefix, func(w http.ResponseWriter, r *http.Request) {
			for _, dir := range fileList {
				fmt.Println("SERVING FILE: ", filepath.Join(dir, r.URL.Path[len(urlPrefix)-1:]))
				reqFile, err := filepath.Abs(filepath.Join(dir, r.URL.Path[len(urlPrefix)-1:]))
				if err != nil {continue}
				_, err = os.Stat(reqFile)
				if err != nil {continue}
				http.ServeFile(w, r, reqFile)
				return
			}
			http.Error(w, "Not found", http.StatusNotFound)
		})
	} else {
		http.Handle(urlPrefix, http.StripPrefix(urlPrefix, http.FileServer(FS(false))))
	}
	if !nobrowse && browse != "" {
		err := browser.OpenURL(fmt.Sprintf("http://localhost:%d/%s", port, browse))
		if err != nil {
			fmt.Printf("Error: %s", err.Error())
		}
	}
	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", addr, port), nil))

}
