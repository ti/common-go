package database

// PageQueryRequest general query conditions, support pagination, sorting, selection, etc.
type PageQueryRequest struct {
	Filters C        `json:"filter,omitempty"`
	Select  []string `json:"select,omitempty"`
	Sort    []string `json:"sort,omitempty"`
	Page    int      `json:"page,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	// disable count when query
	NoCount bool
}

// PageQueryResponse paging query response
type PageQueryResponse[T any] struct {
	Data  []*T  `json:"data,omitempty"`
	Total int64 `json:"total,omitempty"`
}

// StreamQueryRequest the stream query
type StreamQueryRequest struct {
	PageToken string `json:"page_token,omitempty"`
	PageField string
	Filters   C        `json:"filter,omitempty"`
	Select    []string `json:"select,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Ascending bool
	// disable count when query
	NoCount bool
}

// StreamResponse Stream query response
type StreamResponse[T any] struct {
	PageToken string `json:"page_token,omitempty"`
	Data      []*T   `json:"data,omitempty"`
	Total     int64  `json:"total,omitempty"`
}
