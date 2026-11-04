// Package browserdns turns off browsers' built-in DNS-over-HTTPS through their policy
// keys while No More DPI is active. A browser in strict ("secure") DoH mode never falls
// back to the system resolver, so with the DoH lock on it would lose all name resolution;
// with DoH off it uses the system resolver, i.e. us. Previous values are saved to disk and
// put back on stop (or on the next start after a crash).
package browserdns

import (
	"encoding/json"
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"

	"nomoredpi/internal/logx"
)

type policy struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Str   string `json:"str,omitempty"` // value we set (string policies)
	DWord uint32 `json:"dword"`         // value we set (dword policies)
	IsStr bool   `json:"isStr"`
	// What was there before.
	Had      bool   `json:"had"`
	OldStr   string `json:"oldStr,omitempty"`
	OldDWord uint32 `json:"oldDword,omitempty"`
	Created  bool   `json:"created"` // we created Key
}

func wanted() []policy {
	var out []policy
	for _, k := range []string{
		`SOFTWARE\Policies\BraveSoftware\Brave`,
		`SOFTWARE\Policies\Google\Chrome`,
		`SOFTWARE\Policies\Microsoft\Edge`,
		`SOFTWARE\Policies\Chromium`,
		`SOFTWARE\Policies\Vivaldi`,
		`SOFTWARE\Policies\Opera Software\Opera`,
	} {
		out = append(out, policy{Key: k, Name: "DnsOverHttpsMode", Str: "off", IsStr: true})
	}
	out = append(out, policy{Key: `SOFTWARE\Policies\Mozilla\Firefox\DNSOverHTTPS`, Name: "Enabled", DWord: 0})
	return out
}

// Apply sets the policies, recording the previous state in statePath first.
func Apply(statePath string) error {
	if _, err := os.Stat(statePath); err == nil {
		// Already applied by this or a crashed run; keep the original snapshot.
		return set(load(statePath))
	}
	list := wanted()
	for i := range list {
		p := &list[i]
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, p.Key, registry.QUERY_VALUE)
		if err != nil {
			p.Created = true
			continue
		}
		if p.IsStr {
			if v, _, err := k.GetStringValue(p.Name); err == nil {
				p.Had, p.OldStr = true, v
			}
		} else if v, _, err := k.GetIntegerValue(p.Name); err == nil {
			p.Had, p.OldDWord = true, uint32(v)
		}
		k.Close()
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	if err := os.WriteFile(statePath, b, 0o644); err != nil {
		return err
	}
	return set(list)
}

func set(list []policy) error {
	var firstErr error
	for _, p := range list {
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, p.Key, registry.SET_VALUE)
		if err != nil {
			firstErr = errors.Join(firstErr, err)
			continue
		}
		if p.IsStr {
			err = k.SetStringValue(p.Name, p.Str)
		} else {
			err = k.SetDWordValue(p.Name, p.DWord)
		}
		k.Close()
		if err != nil {
			firstErr = errors.Join(firstErr, err)
		}
	}
	logx.Printf("browserdns: built-in DoH turned off for Chromium browsers and Firefox")
	return firstErr
}

// load reads the saved snapshot, keeping only the policies wanted() names and taking the
// values we set from wanted(), not the file: the service writes HKLM as SYSTEM, so a
// tampered state file must not be able to point it at any other key or value.
func load(statePath string) []policy {
	var saved []policy
	b, err := os.ReadFile(statePath)
	if err == nil {
		_ = json.Unmarshal(b, &saved)
	}
	allowed := map[string]policy{}
	for _, w := range wanted() {
		allowed[w.Key+`\`+w.Name] = w
	}
	var list []policy
	for _, p := range saved {
		w, ok := allowed[p.Key+`\`+p.Name]
		if !ok {
			continue
		}
		p.Str, p.DWord, p.IsStr = w.Str, w.DWord, w.IsStr
		list = append(list, p)
	}
	return list
}

// Restore puts the previous values back and removes the state file.
func Restore(statePath string) {
	list := load(statePath)
	if len(list) == 0 {
		os.Remove(statePath)
		return
	}
	for _, p := range list {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, p.Key, registry.SET_VALUE)
		if err != nil {
			continue
		}
		switch {
		case p.Had && p.IsStr:
			_ = k.SetStringValue(p.Name, p.OldStr)
		case p.Had:
			_ = k.SetDWordValue(p.Name, p.OldDWord)
		default:
			_ = k.DeleteValue(p.Name)
		}
		k.Close()
		if p.Created {
			deleteIfEmpty(p.Key)
		}
	}
	os.Remove(statePath)
	logx.Printf("browserdns: browser DoH policies restored")
}

// deleteIfEmpty removes a key we created (and empty parents up to SOFTWARE\Policies).
func deleteIfEmpty(path string) {
	for path != `SOFTWARE\Policies` && path != "" {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			return
		}
		st, err := k.Stat()
		k.Close()
		if err != nil || st.SubKeyCount > 0 || st.ValueCount > 0 {
			return
		}
		if registry.DeleteKey(registry.LOCAL_MACHINE, path) != nil {
			return
		}
		i := len(path) - 1
		for i >= 0 && path[i] != '\\' {
			i--
		}
		if i < 0 {
			return
		}
		path = path[:i]
	}
}
