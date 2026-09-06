package datasets

import (
	"context"
	"unicode/utf8"

	"github.com/oschwald/maxminddb-golang/v2/mmdbdata"
)

type mmdbRecordVerifier struct {
	ctx       context.Context
	remaining int
}

func (verifier *mmdbRecordVerifier) UnmarshalMaxMindDBCursor(cursor mmdbdata.Cursor) (mmdbdata.Cursor, error) {
	return verifier.walk(cursor, 0)
}
func (verifier *mmdbRecordVerifier) walk(cursor mmdbdata.Cursor, depth int) (mmdbdata.Cursor, error) {
	verifier.remaining--
	if verifier.remaining < 0 || depth > 16 {
		return mmdbdata.Cursor{}, exhausted("MMDB record complexity")
	}
	if verifier.remaining%32 == 0 {
		if err := checkContext(verifier.ctx); err != nil {
			return mmdbdata.Cursor{}, err
		}
	}
	kind, err := cursor.Kind()
	if err != nil {
		return mmdbdata.Cursor{}, err
	}
	switch kind {
	case mmdbdata.KindMap:
		fields, err := cursor.Map()
		if err != nil {
			return mmdbdata.Cursor{}, err
		}
		if fields.Size() > 256 {
			return mmdbdata.Cursor{}, exhausted("MMDB map fields")
		}
		seen := make(map[string]bool, int(fields.Size()))
		var next mmdbdata.Cursor
		for {
			key, value, ok := fields.Next(next)
			if !ok {
				break
			}
			if len(key) > 128 || !utf8.Valid(key) || seen[string(key)] {
				return mmdbdata.Cursor{}, invalid("MMDB map key")
			}
			seen[string(key)] = true
			next, err = verifier.walk(value, depth+1)
			if err != nil {
				return mmdbdata.Cursor{}, err
			}
		}
		return fields.End()
	case mmdbdata.KindSlice:
		values, err := cursor.Slice()
		if err != nil {
			return mmdbdata.Cursor{}, err
		}
		size, err := values.Size()
		if err != nil {
			return mmdbdata.Cursor{}, err
		}
		if size > 256 {
			return mmdbdata.Cursor{}, exhausted("MMDB array entries")
		}
		var next mmdbdata.Cursor
		for {
			_, value, ok := values.Next(next)
			if !ok {
				break
			}
			next, err = verifier.walk(value, depth+1)
			if err != nil {
				return mmdbdata.Cursor{}, err
			}
		}
		return values.End()
	case mmdbdata.KindString:
		value, next, err := cursor.ReadString()
		if err == nil && (len(value) > 65536 || !utf8.ValidString(value)) {
			return mmdbdata.Cursor{}, invalid("MMDB bounded UTF-8 string")
		}
		return next, err
	case mmdbdata.KindBytes:
		value, next, err := cursor.ReadBytes()
		if err == nil && len(value) > 65536 {
			return mmdbdata.Cursor{}, exhausted("MMDB bytes value")
		}
		return next, err
	case mmdbdata.KindBool:
		_, next, err := cursor.ReadBool()
		return next, err
	case mmdbdata.KindFloat32, mmdbdata.KindFloat64:
		_, next, err := cursor.ReadFloat()
		return next, err
	case mmdbdata.KindInt32, mmdbdata.KindUint16, mmdbdata.KindUint32, mmdbdata.KindUint64:
		_, _, next, err := cursor.ReadInteger()
		return next, err
	case mmdbdata.KindUint128:
		_, _, next, err := cursor.ReadUint128()
		return next, err
	default:
		return mmdbdata.Cursor{}, invalid("MMDB record kind")
	}
}
