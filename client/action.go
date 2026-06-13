package client

import "github.com/jxsl13/twclient/packet"

// Action is a protocol-independent command a consumer (UI input or ML policy)
// applies to the client via Do (V18, V22). One action set maps to both 0.6 and
// 0.7 sends; consumers never branch on protocol.
type Action interface{ actionTag() }

// ActInput sends a player input (movement, aim, jump, hook, fire, weapon).
type ActInput struct{ Input packet.PlayerInput }

// ActChat sends a chat line; Team selects team vs all chat.
type ActChat struct {
	Team bool
	Msg  string
}

// ActWhisper sends a private message to a client.
type ActWhisper struct {
	ToID int
	Msg  string
}

// ActEmoticon shows an emoticon.
type ActEmoticon struct{ Emoticon packet.Emoticon }

// ActKill requests self-kill (/kill).
type ActKill struct{}

// ActVote casts a yes/no vote on the running vote.
type ActVote struct{ Approve bool }

// ActCallVote starts a vote.
type ActCallVote struct {
	Type   string
	Value  string
	Reason string
}

// ActSetTeam requests a team change.
type ActSetTeam struct{ Team int }

// ActSpectate sets the spectated client (-1 = free-view).
type ActSpectate struct{ TargetID int }

func (ActInput) actionTag()    {}
func (ActChat) actionTag()     {}
func (ActWhisper) actionTag()  {}
func (ActEmoticon) actionTag() {}
func (ActKill) actionTag()     {}
func (ActVote) actionTag()     {}
func (ActCallVote) actionTag() {}
func (ActSetTeam) actionTag()  {}
func (ActSpectate) actionTag() {}

// sendInputDirect sends an input for the current predicted tick without the
// per-tick-boundary throttle of SendInput. The tick driver (or a consumer)
// controls cadence, so Do(ActInput) applies the input immediately.
func (c *Client) sendInputDirect(input packet.PlayerInput) error {
	predTick := c.predTime.PredTick()
	ackTick := c.predTime.AckTick()
	if predTick > 0 {
		c.predInputs.record(predTick, input)
	}
	data := packInput(&input)
	return c.sess.SendInput(ackTick, predTick, inputSize, data)
}

// Do applies one action to the server through the active session. This is the
// single action path shared by UI input and ML output (V20).
func (c *Client) Do(a Action) error {
	if c.sess == nil {
		return ErrNotConnected
	}
	switch act := a.(type) {
	case ActInput:
		return c.sendInputDirect(act.Input)
	case ActChat:
		return c.sess.SendChatTeam(act.Team, act.Msg)
	case ActWhisper:
		return c.sess.SendWhisper(act.ToID, act.Msg)
	case ActEmoticon:
		return c.sess.SendEmoticon(act.Emoticon)
	case ActKill:
		return c.sess.SendKill()
	case ActVote:
		return c.sess.SendVote(act.Approve)
	case ActCallVote:
		return c.sess.SendCallVote(act.Type, act.Value, act.Reason)
	case ActSetTeam:
		return c.sess.SendSetTeam(act.Team)
	case ActSpectate:
		return c.sess.SendSpectate(act.TargetID)
	default:
		return nil
	}
}
