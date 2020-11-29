package cache

import (
	"encoding/gob"
	"hash/fnv"
	"io"
	"reflect"
	"strings"
)

// Dump saves cached entries and returns a number of processed entries.
func (c *memory) Dump(w io.Writer) (int, error) {
	encoder := gob.NewEncoder(w)

	// TODO check if Entry escapes and if value semantics helps.
	return c.Walk(func(key string, value Entry) error {
		return encoder.Encode(struct {
			Key   string
			Entry entry
		}{
			Key:   key,
			Entry: value.(entry),
		})
	})
}

// Restore loads cached entries and returns number of processed entries.
func (c *memory) Restore(r io.Reader) (int, error) {
	decoder := gob.NewDecoder(r)
	e := struct {
		Key   string
		Entry entry
	}{}
	n := 0

	for {
		err := decoder.Decode(&e)
		if err == io.EOF {
			break
		}

		if err != nil {
			return n, err
		}

		c.Lock()
		c.data[e.Key] = e.Entry
		c.Unlock()

		n++
	}

	return n, nil
}

var gobTypesHash uint64

// GobTypesHashReset resets types hash to zero value.
func GobTypesHashReset() {
	gobTypesHash = 0
}

// GobTypesHash returns a fingerprint of a group of types to transfer.
func GobTypesHash() uint64 {
	return gobTypesHash
}

// GobRegister enables cached type transferring.
func GobRegister(values ...interface{}) {
	for _, value := range values {
		h := fnv.New64()
		t := reflect.TypeOf(value)
		// nolint:errcheck // fnv.Write never returns an error.
		_, _ = h.Write([]byte(t.PkgPath() + t.String()))
		recursiveTypeHash(t, h, map[reflect.Type]bool{})
		gobTypesHash ^= h.Sum64()

		gob.Register(value)
	}
}

// RecursiveTypeHash hashes type of value recursively to ensure structural match.
func recursiveTypeHash(t reflect.Type, h io.Writer, met map[reflect.Type]bool) {
	for {
		if t.Kind() != reflect.Ptr {
			break
		}

		t = t.Elem()
	}

	if met[t] {
		return
	}

	met[t] = true

	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)

			// Skip unexported field.
			if f.Name != "" && (f.Name[0:1] == strings.ToLower(f.Name[0:1])) {
				continue
			}

			if !f.Anonymous {
				// nolint:errcheck // fnv.Write never returns an error.
				_, _ = h.Write([]byte(f.Name))
			}

			recursiveTypeHash(f.Type, h, met)
		}

	case reflect.Slice, reflect.Array:
		recursiveTypeHash(t.Elem(), h, met)
	case reflect.Map:
		recursiveTypeHash(t.Key(), h, met)
		recursiveTypeHash(t.Elem(), h, met)
	default:
		// nolint:errcheck // fnv.Write never returns an error.
		_, _ = h.Write([]byte(t.String()))
	}
}

// nolint:gochecknoinits // Registering types to a package level registry of "encoding/gob".
func init() {
	// Registering commonly used types.
	gob.Register(map[string]interface{}{})
	gob.Register([]interface{}{})
}
