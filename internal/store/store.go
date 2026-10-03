package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"sandboxctl/internal/config"
	"sandboxctl/internal/model"
	"sandboxctl/internal/names"
)

var ErrNotFound = errors.New("sandbox does not exist")

type Store struct {
	Paths config.Paths
}

func (s Store) Save(instance model.Instance) error {
	if err := names.Validate(instance.Name); err != nil {
		return err
	}
	dir := s.Paths.InstanceDir(instance.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the instance directory: %w", err)
	}

	data, err := json.MarshalIndent(instance, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the instance record: %w", err)
	}
	data = append(data, '\n')

	temporary := filepath.Join(dir, ".instance.json.tmp")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write the temporary instance record: %w", err)
	}
	if err := os.Rename(temporary, s.Paths.InstanceFile(instance.Name)); err != nil {
		return fmt.Errorf("replace the instance record: %w", err)
	}
	return nil
}

func (s Store) Load(name string) (model.Instance, error) {
	if err := names.Validate(name); err != nil {
		return model.Instance{}, err
	}
	data, err := os.ReadFile(s.Paths.InstanceFile(name))
	if errors.Is(err, fs.ErrNotExist) {
		return model.Instance{}, ErrNotFound
	}
	if err != nil {
		return model.Instance{}, fmt.Errorf("read the instance record: %w", err)
	}

	var instance model.Instance
	if err := json.Unmarshal(data, &instance); err != nil {
		return model.Instance{}, fmt.Errorf("decode the instance record: %w", err)
	}
	if instance.SchemaVersion != model.SchemaVersion {
		return model.Instance{}, fmt.Errorf("unsupported instance schema version %d", instance.SchemaVersion)
	}
	return instance, nil
}

func (s Store) List() ([]model.Instance, error) {
	entries, err := os.ReadDir(s.Paths.Instances)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the instances directory: %w", err)
	}

	instances := make([]model.Instance, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instance, err := s.Load(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", entry.Name(), err)
		}
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].Name < instances[j].Name
	})
	return instances, nil
}
