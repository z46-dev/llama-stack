package modelprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

type (
	Catalog struct {
		Profiles []Profile `toml:"profile"`
	}

	Profile struct {
		Name               string         `toml:"name"`
		ModelGlob          string         `toml:"model_glob"`
		ChatTemplateFile   string         `toml:"chat_template_file"`
		ChatTemplateKwargs map[string]any `toml:"chat_template_kwargs"`
	}
)

// Render writes llama.cpp router presets for installed models with compatibility profiles.
func Render(catalogPath, modelsDirectory, presetPath string) (matched int, err error) {
	var (
		catalog Catalog
		entries []os.DirEntry
		lines   []string = []string{"version = 1", ""}
	)

	if _, err = toml.DecodeFile(catalogPath, &catalog); err != nil {
		err = fmt.Errorf("decode model profiles: %w", err)
		return
	}
	if err = validate(catalog); err != nil {
		return
	}
	if entries, err = os.ReadDir(modelsDirectory); err != nil {
		err = fmt.Errorf("read models directory: %w", err)
		return
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})

	for _, entry := range entries {
		var profile *Profile

		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".gguf") {
			continue
		}
		if profile, err = matchingProfile(catalog.Profiles, entry.Name()); err != nil {
			return
		}
		if profile == nil {
			continue
		}
		if _, err = os.Stat(profile.ChatTemplateFile); err != nil {
			err = fmt.Errorf("profile %q template: %w", profile.Name, err)
			return
		}
		lines = append(lines,
			"["+strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))+"]",
			"chat-template-file = "+profile.ChatTemplateFile,
		)
		if len(profile.ChatTemplateKwargs) > 0 {
			var encoded []byte
			if encoded, err = json.Marshal(profile.ChatTemplateKwargs); err != nil {
				err = fmt.Errorf("encode profile %q template arguments: %w", profile.Name, err)
				return
			}
			lines = append(lines, "chat-template-kwargs = "+string(encoded))
		}
		lines = append(lines, "")
		matched++
	}

	err = writeAtomic(presetPath, []byte(strings.Join(lines, "\n")))
	return
}

// validate rejects ambiguous or unsafe profile definitions.
func validate(catalog Catalog) (err error) {
	for _, profile := range catalog.Profiles {
		if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.ModelGlob) == "" {
			err = errors.New("model profile name and model_glob must not be empty")
			return
		}
		if strings.ContainsAny(profile.ModelGlob, `/\\`) {
			err = fmt.Errorf("profile %q model_glob must match a file name", profile.Name)
			return
		}
		if _, err = path.Match(profile.ModelGlob, "model.gguf"); err != nil {
			err = fmt.Errorf("profile %q model_glob: %w", profile.Name, err)
			return
		}
		if !filepath.IsAbs(profile.ChatTemplateFile) || strings.ContainsAny(profile.ChatTemplateFile, "\r\n") {
			err = fmt.Errorf("profile %q chat_template_file must be an absolute single-line path", profile.Name)
			return
		}
	}

	return
}

func matchingProfile(profiles []Profile, modelName string) (matched *Profile, err error) {
	for index := range profiles {
		var matches bool

		if matches, err = path.Match(profiles[index].ModelGlob, modelName); err != nil {
			return
		}
		if !matches {
			continue
		}
		if matched != nil {
			err = fmt.Errorf("model %q matches profiles %q and %q", modelName, matched.Name, profiles[index].Name)
			return
		}
		matched = &profiles[index]
	}

	return
}

func writeAtomic(destination string, data []byte) (err error) {
	var temporary *os.File

	if err = os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return
	}
	if temporary, err = os.CreateTemp(filepath.Dir(destination), ".model-presets-*"); err != nil {
		return
	}
	defer func() {
		_ = os.Remove(temporary.Name())
	}()

	if err = temporary.Chmod(0o640); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if err == nil {
		err = temporary.Close()
	}
	if err == nil {
		err = os.Rename(temporary.Name(), destination)
	}

	return
}
