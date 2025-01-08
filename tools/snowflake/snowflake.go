package snowflake

import (
	"crypto/rand"
	"hash/fnv"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/bwmarrin/snowflake"
)

var snowflakeNode *snowflake.Node

func init() {
	nodeNumber := getNodeNumber()
	regionIDStr := os.Getenv("REGION_ID")
	if regionIDStr != "" {
		regionID, _ := strconv.Atoi(regionIDStr)
		if regionID > 0 {
			// Limits: Up to 99 nodes per cluster.
			// cluster_0: 0, 1,2,3,4,~ 99
			// cluster_1: 100, 101, 102,103,104,~ 199
			nodeNumber = int64(regionID*100) + nodeNumber
		}
	}
	var err error
	snowflakeNode, err = snowflake.NewNode(nodeNumber)
	if err != nil {
		panic("init snowflake error " + err.Error())
	}
}

// ID new simple snowflake id.
func ID() int64 {
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
	if number > 1023 || number < 0 {
		return getHostHashNumber(hostname)
	}
	return int64(number)
}

func getHostHashNumber(hostname string) int64 {
	if hostname == "" {
		randNumber, err := rand.Int(rand.Reader, big.NewInt(1023))
		if err != nil {
			return 0
		}
		return randNumber.Int64()
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(hostname))
	return int64(h.Sum32() % 1023)
}
