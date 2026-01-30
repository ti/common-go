package snowflake

import (
	"crypto/rand"
	"hash/fnv"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/bwmarrin/snowflake"
)

const (
	// MaxNodeNumber is the maximum node number (10 bits = 1024)
	MaxNodeNumber = 1023
	// NodesPerRegion defines how many nodes are allowed per region
	NodesPerRegion = 100
)

var (
	snowflakeNode *snowflake.Node
	initOnce      sync.Once
	initErr       error
)

func init() {
	initOnce.Do(func() {
		nodeNumber := getNodeNumber()
		regionIDStr := os.Getenv("REGION_ID")
		if regionIDStr != "" {
			regionID, err := strconv.Atoi(regionIDStr)
			if err == nil && regionID > 0 {
				// Limits: Up to 99 nodes per region.
				// region_0: 0-99
				// region_1: 100-199
				nodeNumber = int64(regionID*NodesPerRegion) + nodeNumber
			}
		}

		snowflakeNode, initErr = snowflake.NewNode(nodeNumber)
		if initErr != nil {
			// Log error instead of panic to allow graceful degradation
			log.Printf("[WARN] Failed to initialize snowflake node %d: %v. ID generation will fail.", nodeNumber, initErr)
		}
	})
}

// ID generates a new snowflake ID.
// Returns 0 if the snowflake node failed to initialize.
func ID() int64 {
	if snowflakeNode == nil {
		if initErr != nil {
			log.Printf("[ERROR] Snowflake not initialized: %v", initErr)
		}
		return 0
	}
	return int64(snowflakeNode.Generate())
}

// getNodeNumber get the node ID, first try to get it from the hostname, if it cannot be got,
// it will be randomly generated.
func getNodeNumber() int64 {
	hostname, err := os.Hostname()
	if err != nil {
		return getHostHashNumber("")
	}
	index := strings.LastIndex(hostname, "-")
	if index <= 0 {
		return getHostHashNumber(hostname)
	}
	hostSuffix := hostname[index+1:]
	if hostSuffix == "" {
		return getHostHashNumber(hostname)
	}
	number, errInt := strconv.Atoi(hostSuffix)
	if errInt != nil {
		return getHostHashNumber(hostname)
	}
	if number > MaxNodeNumber || number < 0 {
		return getHostHashNumber(hostname)
	}
	return int64(number)
}

func getHostHashNumber(hostname string) int64 {
	if hostname == "" {
		randNumber, err := rand.Int(rand.Reader, big.NewInt(MaxNodeNumber))
		if err != nil {
			log.Printf("[WARN] Failed to generate random node number: %v", err)
			return 0
		}
		return randNumber.Int64()
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(hostname))
	return int64(h.Sum32() % MaxNodeNumber)
}
