package enrich

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"go-peek/pkg/models/events"
	"go-peek/pkg/models/meta"
	"go-peek/pkg/persist"
	"go-peek/pkg/providentia"
)

const badgerPrefix = "assets"

var ErrMissingPersist = errors.New("missing badgerdb persistance")

type ErrMissingAssetData struct{ Event events.GameEvent }

func (e ErrMissingAssetData) Error() string {
	return fmt.Sprintf("missing asset data for %+v", e.Event)
}

type Config struct {
	Persist *persist.Badger
}

func (c Config) Validate() error {
	if c.Persist == nil {
		return ErrMissingPersist
	}
	return nil
}

type Counts struct {
	Events      uint
	MissingMeta uint

	AssetPickups uint
	AssetUpdates uint
	Assets       int

	ParseErrs countsParseErrs
}

type countsParseErrs struct {
	Suricata uint
	Windows  uint
	Syslog   uint
	Snoopy   uint
}

type Handler struct {
	Counts

	missingLookupSet map[string]bool

	assets  map[string]providentia.Record
	persist *persist.Badger
}

func (h Handler) MissingKeys() []string {
	keys := make([]string, 0, len(h.missingLookupSet))
	for key := range h.missingLookupSet {
		keys = append(keys, key)
	}
	return keys
}

func (h *Handler) AddAsset(value providentia.Record) *Handler {
	h.AssetPickups++
	for _, key := range value.Keys() {
		_, ok := h.assets[key]
		if !ok {
			h.Counts.AssetUpdates++
			h.persist.Set(badgerPrefix, persist.GenericValue{Key: key, Data: value})
			h.assets[key] = value
		}
	}
	h.Counts.Assets = len(h.assets)
	return h
}

func (h *Handler) Decode(raw []byte, kind events.Atomic) (events.GameEvent, error) {
	var event events.GameEvent
	h.Counts.Events++

	switch kind {
	case events.SuricataE:
		var obj events.Suricata
		if err := json.Unmarshal(raw, &obj); err != nil {
			h.Counts.ParseErrs.Suricata++
			return nil, err
		}
		event = &obj
	case events.EventLogE, events.SysmonE:
		var obj events.DynamicWinlogbeat
		if err := json.Unmarshal(raw, &obj); err != nil {
			h.Counts.ParseErrs.Windows++
			return nil, err
		}
		event = &obj
	case events.SyslogE:
		var obj events.Syslog
		if err := json.Unmarshal(raw, &obj); err != nil {
			h.Counts.ParseErrs.Syslog++
			return nil, err
		}
		event = &obj
	case events.SnoopyE:
		var obj events.Snoopy
		if err := json.Unmarshal(raw, &obj); err != nil {
			h.Counts.ParseErrs.Snoopy++
			return nil, err
		}
		event = &obj
	}
	return event, nil
}

func (h *Handler) Enrich(event events.GameEvent) error {
	fullAsset := event.GetAsset()
	if fullAsset == nil {
		return ErrMissingAssetData{event}
	}

	fullAsset.Asset = *h.assetLookup(fullAsset.Asset)
	if fullAsset.Source != nil {
		fullAsset.Source = h.assetLookup(*fullAsset.Source)
	}
	if fullAsset.Destination != nil {
		fullAsset.Destination = h.assetLookup(*fullAsset.Destination)
	}

	return nil
}

func (h Handler) assetLookup(asset meta.Asset) *meta.Asset {
	if asset.IP != nil {
		if val, ok := h.assets[asset.IP.String()]; ok {
			return val.Asset()
		}
	}
	if asset.Host != "" {
		if val, ok := h.assets[asset.Host]; ok {
			return val.Asset()
		}
		h.missingLookupSet[asset.Host] = true
	}
	return &asset
}

func (h *Handler) Close() error {
	return nil
}

func NewHandler(c Config) (*Handler, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	assets := make(map[string]providentia.Record)
	records := c.Persist.Scan(badgerPrefix)
	for record := range records {
		var obj providentia.Record
		buf := bytes.NewBuffer(record.Data)
		err := gob.NewDecoder(buf).Decode(&obj)
		if err != nil {
			return nil, err
		}
		for _, key := range obj.Keys() {
			assets[key] = obj
		}
	}
	return &Handler{
		persist:          c.Persist,
		assets:           assets,
		missingLookupSet: make(map[string]bool),
		Counts:           Counts{Assets: len(assets)},
	}, nil
}
