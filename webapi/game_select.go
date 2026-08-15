// webapi/game_select.go
package main

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"

	"simple_game/game/pkg/registry"
)

// 分配策略常量。
const (
	selectFixed = "fixed" // 指定节点：固定路由到 GameSelect.FixedNode
	selectHash  = "hash"  // 哈希分配：按玩家名哈希到存活节点（空值默认）
)

// selectGame 按配置策略为玩家选择一个 game 节点。
func (h *loginHandler) selectGame(name string) (string, error) {
	strategy := h.gameSelect.Strategy
	if strategy == "" {
		strategy = selectHash
	}
	switch strategy {
	case selectFixed:
		if h.gameSelect.FixedNode == "" {
			return "", errors.New("GameSelect.Strategy=fixed 但 FixedNode 为空")
		}
		return h.gameSelect.FixedNode, nil
	case selectHash:
		return h.selectHash(name)
	default:
		return "", fmt.Errorf("unknown GameSelect.Strategy: %s", strategy)
	}
}

// selectHash 从存活 game 列表中按玩家名哈希稳定选一个节点。
func (h *loginHandler) selectHash(name string) (string, error) {
	games, err := registry.ListFresh(context.Background(), h.rdb)
	if err != nil {
		return "", err
	}
	if len(games) == 0 {
		return "", errors.New("no game server available")
	}
	// 稳定排序，保证同一玩家跨登录哈希到同一节点（SMembers 顺序不定）
	sort.Strings(games)
	idx := int(hashName(name)) % len(games)
	return games[idx], nil
}

// hashName FNV-1a 32 位哈希，与 game 侧 dispatch/pool.go 同一算法。
func hashName(name string) uint32 {
	hh := fnv.New32a()
	_, _ = hh.Write([]byte(name))
	return hh.Sum32()
}
