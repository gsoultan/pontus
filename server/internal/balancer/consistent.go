package balancer

import (
	"context"
	"fmt"
	"hash/crc32"
	"slices"
	"sort"
	"sync"

	"github.com/gsoultan/pontus/server/internal/pool"
)

const virtualNodes = 100

type nodeHash struct {
	hash uint32
	node pool.Backend
}

type ConsistentHash struct {
	mu    sync.RWMutex
	nodes []pool.Backend
	ring  []nodeHash
}

func NewConsistentHash(nodes []pool.Backend) *ConsistentHash {
	c := &ConsistentHash{}
	c.UpdateNodes(nodes)
	return c
}

func (c *ConsistentHash) Next(ctx context.Context, hint Hint) (pool.Backend, error) {
	c.mu.RLock()
	ring := c.ring
	nodes := c.nodes
	c.mu.RUnlock()

	if len(nodes) == 0 {
		return nil, ErrNoHealthyBackends
	}

	ptr := FilterNodes(nodes, hint)
	defer PutTargets(ptr)
	targets := *ptr

	if len(targets) == 0 {
		return nil, ErrNoHealthyBackends
	}

	if hint.Key == "" {
		// Fallback to Weighted Least Connections if no key
		var best pool.Backend
		minCost := -1.0
		for _, node := range targets {
			cost := CalculateCost(node, hint.CallerZone)
			if minCost < 0 || cost < minCost {
				minCost = cost
				best = node
			}
		}
		return best, nil
	}

	keyHash := crc32.ChecksumIEEE([]byte(hint.Key))
	idx := sort.Search(len(ring), func(i int) bool {
		return ring[i].hash >= keyHash
	})

	if idx == len(ring) {
		idx = 0
	}

	// Find the first healthy node in the ring starting from idx
	for range len(ring) {
		node := ring[idx].node
		// Check if node is healthy and matches the hint (role)
		// Instead of slices.Contains which is O(N), we can just check if node is in targets
		// but targets is already filtered by Role and Health.
		if slices.Contains(targets, node) {
			return node, nil
		}
		idx = (idx + 1) % len(ring)
	}

	return targets[0], nil
}

func (c *ConsistentHash) UpdateNodes(nodes []pool.Backend) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nodes = slices.Clone(nodes)
	var ring []nodeHash

	for _, node := range nodes {
		weight := node.Weight()
		if weight <= 0 {
			weight = 1
		}
		// Adjust virtual nodes by weight
		count := virtualNodes * weight
		for i := range count {
			key := fmt.Sprintf("%s#%d", node.Address(), i)
			hash := crc32.ChecksumIEEE([]byte(key))
			ring = append(ring, nodeHash{hash: hash, node: node})
		}
	}

	sort.Slice(ring, func(i, j int) bool {
		return ring[i].hash < ring[j].hash
	})

	c.ring = ring
}

func (c *ConsistentHash) Name() string {
	return "Consistent Hash"
}
