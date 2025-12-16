package types

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"

	"github.com/davecgh/go-spew/spew"
)

const (
	alphanums = "bcdfghjklmnpqrstvwxz2456789"
)

func ComputeStructHash(template interface{}, collisionCount *int32) string {
	templateSpecHasher := fnv.New32a()
	DeepHashObject(templateSpecHasher, template)

	// Add collisionCount in the hash if it exists.
	if collisionCount != nil {
		collisionCountBytes := make([]byte, 8)
		binary.LittleEndian.PutUint32(collisionCountBytes, uint32(*collisionCount))
		_, _ = templateSpecHasher.Write(collisionCountBytes)
	}

	return SafeEncodeString(fmt.Sprint(templateSpecHasher.Sum32()))
}

func DeepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	_ = printer.Fprintf(hasher, "%#v", objectToWrite)
}

func SafeEncodeString(s string) string {
	r := make([]byte, len(s))
	for i, b := range []rune(s) {
		r[i] = alphanums[(int(b) % len(alphanums))]
	}
	return string(r)
}
