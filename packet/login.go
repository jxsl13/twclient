package packet

// Login defaults match the DDNet/Teeworlds client defaults used when a value
// is not provided.
const (
	// DefaultName is the Teeworlds default player name.
	DefaultName = "nameless tee"
	// DefaultSkin is the Teeworlds default tee skin.
	DefaultSkin = "default"
	// DefaultCountry is the "no country" sentinel (Teeworlds uses -1).
	DefaultCountry = -1
)

// LoginConfig holds the optional parameters for a session login. It is
// populated by LoginOption values so that callers only set what they need
// (e.g. a server password) rather than passing every field positionally.
// Unset fields fall back to the DDNet/Teeworlds defaults.
type LoginConfig struct {
	// Skin is the tee skin name. Defaults to DefaultSkin.
	Skin string
	// Country is the player country code. Defaults to DefaultCountry (-1).
	Country int
	// Password is sent in the NETMSG_INFO handshake message. Empty means an
	// unprotected server.
	Password string
}

// LoginOption configures an optional login parameter. Both net6 and net7
// sessions accept these, keeping the Login surface protocol-unified.
type LoginOption func(*LoginConfig)

// WithLoginSkin sets the tee skin name used during login.
func WithLoginSkin(skin string) LoginOption {
	return func(c *LoginConfig) { c.Skin = skin }
}

// WithLoginCountry sets the player country code used during login.
func WithLoginCountry(country int) LoginOption {
	return func(c *LoginConfig) { c.Country = country }
}

// WithLoginPassword sets the server password used during login.
func WithLoginPassword(password string) LoginOption {
	return func(c *LoginConfig) { c.Password = password }
}

// ApplyLoginOptions builds a LoginConfig from the DDNet/Teeworlds defaults plus
// the given options. Sessions call this at the top of Login.
func ApplyLoginOptions(opts ...LoginOption) LoginConfig {
	cfg := LoginConfig{Skin: DefaultSkin, Country: DefaultCountry}
	for _, opt := range opts {
		if opt != nil { // a nil option is ignored (V70)
			opt(&cfg)
		}
	}
	return cfg
}
