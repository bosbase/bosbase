package core

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"

	"github.com/bosbase/bosbase-enterprise/core/validators"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

func init() {
	Fields[FieldTypeVector] = func() Field {
		return &VectorField{}
	}
}

const FieldTypeVector = "vector"

var (
	_ Field        = (*VectorField)(nil)
	_ DriverValuer = (*VectorField)(nil)
)

// VectorField defines "vector" type field for storing pgvector embeddings.
//
// The field value is stored as a pgvector column (vector(N)) and can be
// set as []float64 or a string in the pgvector format "[0.1,0.2,0.3]".
//
// Examples of updating a record's VectorField value programmatically:
//
//	record.Set("embedding", []float64{0.1, 0.2, 0.3})
//	record.Set("embedding", "[0.1,0.2,0.3]")
type VectorField struct {
	// Name (required) is the unique name of the field.
	Name string `form:"name" json:"name"`

	// Id is the unique stable field identifier.
	//
	// It is automatically generated from the name when adding to a collection FieldsList.
	Id string `form:"id" json:"id"`

	// System prevents the renaming and removal of the field.
	System bool `form:"system" json:"system"`

	// Hidden hides the field from the API response.
	Hidden bool `form:"hidden" json:"hidden"`

	// Presentable hints the Dashboard UI to use the underlying
	// field record value in the relation preview label.
	Presentable bool `form:"presentable" json:"presentable"`

	// ---

	// Required will require the field value to be a non-empty vector.
	Required bool `form:"required" json:"required"`

	// Dimension specifies the number of dimensions in the vector.
	// Defaults to 1536 (OpenAI embedding size) if not set.
	Dimension int `form:"dimension" json:"dimension"`

	// Distance specifies the pgvector distance metric used for similarity search.
	// Valid values for the float vector(N) type: "cosine", "l2", "inner_product", "l1".
	// Defaults to "cosine".
	Distance string `form:"distance" json:"distance"`
}

// Type implements [Field.Type] interface method.
func (f *VectorField) Type() string {
	return FieldTypeVector
}

// GetId implements [Field.GetId] interface method.
func (f *VectorField) GetId() string {
	return f.Id
}

// SetId implements [Field.SetId] interface method.
func (f *VectorField) SetId(id string) {
	f.Id = id
}

// GetName implements [Field.GetName] interface method.
func (f *VectorField) GetName() string {
	return f.Name
}

// SetName implements [Field.SetName] interface method.
func (f *VectorField) SetName(name string) {
	f.Name = name
}

// GetSystem implements [Field.GetSystem] interface method.
func (f *VectorField) GetSystem() bool {
	return f.System
}

// SetSystem implements [Field.SetSystem] interface method.
func (f *VectorField) SetSystem(system bool) {
	f.System = system
}

// GetHidden implements [Field.GetHidden] interface method.
func (f *VectorField) GetHidden() bool {
	return f.Hidden
}

// SetHidden implements [Field.SetHidden] interface method.
func (f *VectorField) SetHidden(hidden bool) {
	f.Hidden = hidden
}

// dimension returns the configured dimension or the default (1536).
func (f *VectorField) dimension() int {
	if f.Dimension <= 0 {
		return 1536
	}
	return f.Dimension
}

// ColumnType implements [Field.ColumnType] interface method.
func (f *VectorField) ColumnType(app App) string {
	return fmt.Sprintf("vector(%d)", f.dimension())
}

// PrepareValue implements [Field.PrepareValue] interface method.
func (f *VectorField) PrepareValue(record *Record, raw any) (any, error) {
	return parseVector(raw)
}

// DriverValue implements [DriverValuer.DriverValue] interface method.
func (f *VectorField) DriverValue(record *Record) (driver.Value, error) {
	val := record.GetRaw(f.Name)
	if val == nil {
		return nil, nil
	}

	v, ok := val.([]float64)
	if !ok {
		return nil, nil
	}

	if len(v) == 0 {
		return nil, nil
	}

	return vectorToString(v), nil
}

// ValidateValue implements [Field.ValidateValue] interface method.
func (f *VectorField) ValidateValue(ctx context.Context, app App, record *Record) error {
	val := record.GetRaw(f.Name)

	if val == nil {
		if f.Required {
			return validation.ErrRequired
		}
		return nil
	}

	v, ok := val.([]float64)
	if !ok {
		return validators.ErrUnsupportedValueType
	}

	if len(v) == 0 {
		if f.Required {
			return validation.ErrRequired
		}
		return nil
	}

	dim := f.dimension()
	if len(v) != dim {
		return validation.NewError(
			"validation_vector_dimension_mismatch",
			fmt.Sprintf("The vector must have exactly %d dimensions, got %d.", dim, len(v)),
		)
	}

	return nil
}

// ValidateSettings implements [Field.ValidateSettings] interface method.
func (f *VectorField) ValidateSettings(ctx context.Context, app App, collection *Collection) error {
	validDistances := []any{"cosine", "l2", "inner_product", "l1"}

	return validation.ValidateStruct(f,
		validation.Field(&f.Id, validation.By(DefaultFieldIdValidationRule)),
		validation.Field(&f.Name, validation.By(DefaultFieldNameValidationRule)),
		validation.Field(&f.Dimension, validation.Min(1)),
		// empty string is allowed (treated as "cosine" at query time)
		validation.Field(&f.Distance, validation.When(f.Distance != "", validation.In(validDistances...))),
	)
}

// vectorToString converts a float64 slice to pgvector string format "[x,y,z]".
func vectorToString(v []float64) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(f, 'f', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// parseVector parses a raw value into a []float64.
// Handles string "[0.1,0.2,0.3]", []float64, and []interface{} inputs.
func parseVector(raw any) ([]float64, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case []float64:
		return v, nil

	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")
		if s == "" {
			return []float64{}, nil
		}
		parts := strings.Split(s, ",")
		result := make([]float64, len(parts))
		for i, p := range parts {
			f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid vector element at index %d: %w", i, err)
			}
			result[i] = f
		}
		return result, nil

	case []interface{}:
		result := make([]float64, len(v))
		for i, elem := range v {
			switch e := elem.(type) {
			case float64:
				result[i] = e
			case float32:
				result[i] = float64(e)
			case int:
				result[i] = float64(e)
			case int64:
				result[i] = float64(e)
			default:
				s := fmt.Sprint(elem)
				f, err := strconv.ParseFloat(s, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid vector element at index %d: %w", i, err)
				}
				result[i] = f
			}
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported vector value type: %T", raw)
	}
}
