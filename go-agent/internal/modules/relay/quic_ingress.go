package relay

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

const (
	relayQUICConnectionIDLength = 16
	relayQUICNonceLength        = 8
	relayQUICClassifierLimit    = 4096
	relayQUICInitialPacket      = 0
)

type relayQUICRemoteState struct {
	owner       ingress.AssociationKey
	established bool
}

type relayQUICRoute struct {
	secret [sha256.Size]byte
	owner  ingress.AssociationKey
	refs   int
}

type relayQUICClassifier struct {
	mu                 sync.Mutex
	byCID              map[string]ingress.AssociationKey
	byRemote           map[string]relayQUICRemoteState
	routes             map[string]relayQUICRoute
	associationRefs    map[ingress.AssociationKey]int
	releaseAssociation func(ingress.AssociationKey)
}

func newRelayQUICClassifier() *relayQUICClassifier {
	return &relayQUICClassifier{
		byCID: make(map[string]ingress.AssociationKey), byRemote: make(map[string]relayQUICRemoteState),
		routes: make(map[string]relayQUICRoute), associationRefs: make(map[ingress.AssociationKey]int),
	}
}

func (c *relayQUICClassifier) setAssociationReleaser(release func(ingress.AssociationKey)) {
	c.mu.Lock()
	c.releaseAssociation = release
	c.mu.Unlock()
}

func (c *relayQUICClassifier) Classify(payload []byte, metadata ingress.PacketMetadata) (ingress.AssociationKey, bool) {
	cid, longHeader, packetType, ok := relayQUICDestinationCID(payload)
	if !ok {
		return ingress.AssociationKey("relay-quic:invalid"), true
	}
	cidKey := hex.EncodeToString(cid)
	remote := ""
	if metadata.RemoteAddr != nil {
		remote = metadata.RemoteAddr.String()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if owner := c.generatedOwnerLocked(cid); owner != "" {
		if remote != "" {
			c.rememberRemoteLocked(remote, owner, !longHeader)
		}
		return owner, true
	}
	if owner := c.byCID[cidKey]; owner != "" {
		if remote != "" {
			c.rememberRemoteLocked(remote, owner, !longHeader)
		}
		return owner, true
	}
	if state, exists := c.byRemote[remote]; exists && remote != "" {
		if !longHeader || packetType != relayQUICInitialPacket || !state.established {
			c.rememberCIDLocked(cidKey, state.owner)
			c.rememberRemoteLocked(remote, state.owner, !longHeader)
			return state.owner, true
		}
	}
	owner := ingress.AssociationKey("relay-quic:" + cidKey)
	c.rememberCIDLocked(cidKey, owner)
	if remote != "" {
		c.rememberRemoteLocked(remote, owner, !longHeader)
	}
	c.enforceLimitLocked()
	return owner, true
}

func (c *relayQUICClassifier) bind(generator *relayGenerationConnectionIDGenerator, remote net.Addr) bool {
	if c == nil || generator == nil || remote == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.byRemote[remote.String()]
	if !ok {
		return false
	}
	route := c.routes[generator.routeID]
	if route.refs == 0 {
		route.secret = generator.secret
		route.owner = state.owner
		c.retainLocked(route.owner)
	}
	route.refs++
	c.routes[generator.routeID] = route
	return true
}

func (c *relayQUICClassifier) release(routeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	route := c.routes[routeID]
	if route.refs > 1 {
		route.refs--
		c.routes[routeID] = route
		return
	}
	delete(c.routes, routeID)
	c.removeOwnerLocked(route.owner)
}

func (c *relayQUICClassifier) generatedOwnerLocked(cid []byte) ingress.AssociationKey {
	if len(cid) != relayQUICConnectionIDLength {
		return ""
	}
	nonce := cid[:relayQUICNonceLength]
	tag := cid[relayQUICNonceLength:]
	for _, route := range c.routes {
		mac := hmac.New(sha256.New, route.secret[:])
		_, _ = mac.Write(nonce)
		if hmac.Equal(tag, mac.Sum(nil)[:relayQUICConnectionIDLength-relayQUICNonceLength]) {
			return route.owner
		}
	}
	return ""
}

func (c *relayQUICClassifier) rememberCIDLocked(key string, owner ingress.AssociationKey) {
	if previous := c.byCID[key]; previous == owner {
		return
	} else if previous != "" {
		c.releaseLocked(previous)
	}
	c.byCID[key] = owner
	c.retainLocked(owner)
}

func (c *relayQUICClassifier) rememberRemoteLocked(key string, owner ingress.AssociationKey, established bool) {
	state, exists := c.byRemote[key]
	if exists && state.owner != owner {
		c.releaseLocked(state.owner)
		c.retainLocked(owner)
	} else if !exists {
		c.retainLocked(owner)
	}
	state.owner = owner
	state.established = state.established || established
	c.byRemote[key] = state
}

func (c *relayQUICClassifier) enforceLimitLocked() {
	for len(c.byCID) > relayQUICClassifierLimit {
		for key, owner := range c.byCID {
			delete(c.byCID, key)
			c.releaseLocked(owner)
			break
		}
	}
	for len(c.byRemote) > relayQUICClassifierLimit {
		for key, state := range c.byRemote {
			delete(c.byRemote, key)
			c.releaseLocked(state.owner)
			break
		}
	}
}

func (c *relayQUICClassifier) removeOwnerLocked(owner ingress.AssociationKey) {
	for key, current := range c.byCID {
		if current == owner {
			delete(c.byCID, key)
			c.releaseLocked(owner)
		}
	}
	for key, state := range c.byRemote {
		if state.owner == owner {
			delete(c.byRemote, key)
			c.releaseLocked(owner)
		}
	}
	c.releaseLocked(owner)
}

func (c *relayQUICClassifier) retainLocked(owner ingress.AssociationKey) {
	if owner != "" {
		c.associationRefs[owner]++
	}
}

func (c *relayQUICClassifier) releaseLocked(owner ingress.AssociationKey) {
	refs := c.associationRefs[owner]
	if refs > 1 {
		c.associationRefs[owner] = refs - 1
		return
	}
	delete(c.associationRefs, owner)
	if refs == 1 && c.releaseAssociation != nil {
		c.releaseAssociation(owner)
	}
}

type relayGenerationConnectionIDGenerator struct {
	secret  [sha256.Size]byte
	routeID string
}

func newRelayGenerationConnectionIDGenerator() (*relayGenerationConnectionIDGenerator, error) {
	generator := &relayGenerationConnectionIDGenerator{}
	if _, err := rand.Read(generator.secret[:]); err != nil {
		return nil, err
	}
	generator.routeID = hex.EncodeToString(generator.secret[:relayQUICNonceLength])
	return generator, nil
}

func (g *relayGenerationConnectionIDGenerator) GenerateConnectionID() (quic.ConnectionID, error) {
	cid := make([]byte, relayQUICConnectionIDLength)
	if _, err := rand.Read(cid[:relayQUICNonceLength]); err != nil {
		return quic.ConnectionID{}, err
	}
	mac := hmac.New(sha256.New, g.secret[:])
	_, _ = mac.Write(cid[:relayQUICNonceLength])
	copy(cid[relayQUICNonceLength:], mac.Sum(nil))
	return quic.ConnectionIDFromBytes(cid), nil
}

func (*relayGenerationConnectionIDGenerator) ConnectionIDLen() int {
	return relayQUICConnectionIDLength
}

func relayQUICDestinationCID(payload []byte) ([]byte, bool, byte, bool) {
	if len(payload) == 0 || payload[0]&0x40 == 0 {
		return nil, false, 0, false
	}
	if payload[0]&0x80 == 0 {
		if len(payload) < 1+relayQUICConnectionIDLength {
			return nil, false, 0, false
		}
		return payload[1 : 1+relayQUICConnectionIDLength], false, 0, true
	}
	if len(payload) < 6 {
		return nil, true, 0, false
	}
	length := int(payload[5])
	if length == 0 || len(payload) < 6+length {
		return nil, true, 0, false
	}
	return payload[6 : 6+length], true, (payload[0] >> 4) & 0x3, true
}

func configureRelayQUICGenerationTransport(transport *quic.Transport, classifier *relayQUICClassifier) error {
	if classifier == nil {
		return nil
	}
	generator, err := newRelayGenerationConnectionIDGenerator()
	if err != nil {
		return err
	}
	transport.ConnectionIDGenerator = generator
	transport.ConnContext = func(connCtx context.Context, info *quic.ClientInfo) (context.Context, error) {
		if classifier.bind(generator, info.RemoteAddr) {
			go func() {
				<-connCtx.Done()
				classifier.release(generator.routeID)
			}()
		}
		return connCtx, nil
	}
	return nil
}
