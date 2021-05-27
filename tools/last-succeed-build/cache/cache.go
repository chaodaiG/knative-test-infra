package cache

import (
	"context"
	"log"
	"strings"
	"sync"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
)

type Client struct {
	project string
	dataset string
	table   string
	bis     map[int64]*BuildInfo
	client  *bigquery.Client
	mutex   sync.Mutex
}

func NewClient(ctx context.Context, project, dataset, table string) (*Client, error) {
	c := &Client{
		project: project,
		dataset: dataset,
		table:   table,
		bis:     make(map[int64]*BuildInfo),
	}
	bqc, err := bigquery.NewClient(ctx, project)
	if err != nil {
		return nil, err
	}
	if _, err := bqc.Dataset(c.dataset).Table(c.table).Metadata(ctx); err != nil {
		if !strings.Contains(err.Error(), "Error 404: Not found:") {
			return nil, err
		}
		log.Printf("Creating %s/%s/%s as it doesn't exist", project, dataset, table)
		schema, newErr := bigquery.InferSchema(BuildInfo{})
		if newErr != nil {
			return nil, err
		}
		err = bqc.Dataset(c.dataset).Table(c.table).Create(ctx, &bigquery.TableMetadata{Schema: schema})
	}
	c.client = bqc
	return c, nil
}

type BuildInfo struct {
	Timestamp int64  `bigquery:""`
	Passed    bool   `bigquery:"passed"`
	BuildID   int64  `bigquery:"build_id"`
	JobName   string `bigquery:"job_name"`
	Updated   bool   `bigquery:"updated"`
}

func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) Load(ctx context.Context) error {
	table := c.client.Dataset(c.dataset).Table(c.table)
	it := table.Read(ctx)
	for {
		var row BuildInfo
		err := it.Next(&row)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return err
		}
		c.bis[row.BuildID] = &row
	}
	return nil
}

func (c *Client) Insert(ctx context.Context, bis []*BuildInfo) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	ins := c.client.Dataset(c.dataset).Table(c.table).Inserter()
	return ins.Put(ctx, bis)
}

func (c *Client) Exist(ctx context.Context, buildID int64) bool {
	_, ok := c.bis[buildID]
	return ok
}

func (c *Client) Updated(ctx context.Context, buildID int64) bool {
	bi, ok := c.bis[buildID]
	if !ok {
		return false
	}
	return bi.Updated
}
