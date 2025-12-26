package core

var _ optionsValidator = (*collectionBaseOptions)(nil)

type collectionBaseOptions struct {
	// ExternalTable marks the collection as mapped to an externally managed SQL table.
	// When enabled, automatic default fields and schema sync are skipped.
	ExternalTable bool `json:"externalTable,omitempty" form:"externalTable"`
}

func (o *collectionBaseOptions) validate(cv *collectionValidator) error {
	return nil
}
