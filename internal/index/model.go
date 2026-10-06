package index

type Track struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Artist       string   `json:"artist"`
	Album        string   `json:"album,omitempty"`
	Duration     int      `json:"duration,omitempty"`
	Format       string   `json:"format"`
	AudioQuality string   `json:"audioQuality"`
	Quality      string   `json:"quality"`
	Tier         string   `json:"tier"`
	BitDepth     int      `json:"bitDepth,omitempty"`
	SampleRate   int      `json:"sampleRate,omitempty"`
	Bitrate      int      `json:"bitrate,omitempty"`
	SizeBytes    int64    `json:"sizeBytes"`
	AudioModes   []string `json:"audioModes,omitempty"`
	AudioMode    string   `json:"audioMode,omitempty"`
	Atmos        *bool    `json:"atmos,omitempty"`
	IsAtmos      bool     `json:"isAtmos,omitempty"`
	HasArtwork   bool     `json:"hasArtwork"`
	MimeType     string   `json:"mimeType"`
	ISRC         string   `json:"isrc,omitempty"`
	Keep         bool     `json:"keep,omitempty"`

	// Pre-computed search indexing fields (not serialized to JSON)
	SearchWords     map[string]struct{} `json:"-"`
	SearchWordArray []string            `json:"-"`
	SearchFullText  string              `json:"-"`
}

type ClientTrack struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Artist          string   `json:"artist"`
	Album           string   `json:"album"`
	Duration        *int     `json:"duration,omitempty"`
	Format          string   `json:"format"`
	AudioQuality    string   `json:"audioQuality"`
	Quality         string   `json:"quality"`
	Tier            string   `json:"tier"`
	BitDepth        int      `json:"bitDepth"`
	SampleRate      int      `json:"sampleRate"`
	AudioModes      []string `json:"audioModes"`
	Atmos           *bool    `json:"atmos,omitempty"`
	ArtworkURL      string   `json:"artworkURL,omitempty"`
	AlbumArtworkURL string   `json:"albumArtworkURL,omitempty"`
	Stream          string   `json:"stream"`
	StreamURL       string   `json:"streamURL"`
	StreamUrl       string   `json:"streamUrl"`
	Audio           string   `json:"audio"`
	AudioUrl        string   `json:"audioUrl"`
	ISRC            string   `json:"isrc,omitempty"`
}

type StreamDescriptor struct {
	URL           string `json:"url"`
	Format        string `json:"format"`
	Codec         string `json:"codec"`
	Container     string `json:"container"`
	Manifest      string `json:"manifest"`
	Encrypted     bool   `json:"encrypted"`
	AudioMode     string `json:"audioMode"`
	SampleRate    int    `json:"sampleRate"`
	BitDepth      int    `json:"bitDepth"`
	Quality       string `json:"quality"`
	AudioQuality  string `json:"audioQuality"`
	Tier          string `json:"tier"`
	StreamQuality string `json:"streamQuality"`
	Description   string `json:"description"`
}

type Manifest struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	Description        string            `json:"description"`
	Icon               string            `json:"icon"`
	CanServeLossless   bool              `json:"canServeLossless"`
	CanServeDolbyAtmos bool              `json:"canServeDolbyAtmos"`
	Resources          []string          `json:"resources"`
	Types              []string          `json:"types"`
	ContentType        string            `json:"contentType"`
	Settings           []ManifestSetting `json:"settings"`
	Endpoints          ManifestEndpoints `json:"endpoints"`
}

type ManifestSetting struct {
	Key     string          `json:"key"`
	Type    string          `json:"type"`
	Default string          `json:"default"`
	Options []SettingOption `json:"options"`
}

type SettingOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type ManifestEndpoints struct {
	Search string `json:"search"`
	ISRC   string `json:"isrc"`
	Stream string `json:"stream"`
	Audio  string `json:"audio"`
}

