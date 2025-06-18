package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/logger"
	"github.com/songquanpeng/one-api/common/random"
)

var (
	TokenCacheSeconds         = config.SyncFrequency
	UserId2GroupCacheSeconds  = config.SyncFrequency
	UserId2QuotaCacheSeconds  = config.SyncFrequency
	UserId2StatusCacheSeconds = config.SyncFrequency
	GroupModelsCacheSeconds   = config.SyncFrequency
)

func CacheGetTokenByKey(key string) (*Token, error) {
	keyCol := "`key`"
	if common.UsingPostgreSQL {
		keyCol = `"key"`
	}
	var token Token
	if !common.RedisEnabled {
		err := DB.Where(keyCol+" = ?", key).First(&token).Error
		return &token, err
	}
	tokenObjectString, err := common.RedisGet(fmt.Sprintf("token:%s", key))
	if err != nil {
		err := DB.Where(keyCol+" = ?", key).First(&token).Error
		if err != nil {
			return nil, err
		}
		jsonBytes, err := json.Marshal(token)
		if err != nil {
			return nil, err
		}
		err = common.RedisSet(fmt.Sprintf("token:%s", key), string(jsonBytes), time.Duration(TokenCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set token error: " + err.Error())
		}
		return &token, nil
	}
	err = json.Unmarshal([]byte(tokenObjectString), &token)
	return &token, err
}

func CacheGetUserGroup(id int) (group string, err error) {
	if !common.RedisEnabled {
		return GetUserGroup(id)
	}
	group, err = common.RedisGet(fmt.Sprintf("user_group:%d", id))
	if err != nil {
		group, err = GetUserGroup(id)
		if err != nil {
			return "", err
		}
		err = common.RedisSet(fmt.Sprintf("user_group:%d", id), group, time.Duration(UserId2GroupCacheSeconds)*time.Second)
		if err != nil {
			logger.SysError("Redis set user group error: " + err.Error())
		}
	}
	return group, err
}

func fetchAndUpdateUserQuota(ctx context.Context, id int) (quota int64, err error) {
	quota, err = GetUserQuota(id)
	if err != nil {
		return 0, err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	if err != nil {
		logger.Error(ctx, "Redis set user quota error: "+err.Error())
	}
	return
}

func CacheGetUserQuota(ctx context.Context, id int) (quota int64, err error) {
	if !common.RedisEnabled {
		return GetUserQuota(id)
	}
	quotaString, err := common.RedisGet(fmt.Sprintf("user_quota:%d", id))
	if err != nil {
		return fetchAndUpdateUserQuota(ctx, id)
	}
	quota, err = strconv.ParseInt(quotaString, 10, 64)
	if err != nil {
		return 0, nil
	}
	if quota <= config.PreConsumedQuota { // when user's quota is less than pre-consumed quota, we need to fetch from db
		logger.Infof(ctx, "user %d's cached quota is too low: %d, refreshing from db", quota, id)
		return fetchAndUpdateUserQuota(ctx, id)
	}
	return quota, nil
}

func CacheUpdateUserQuota(ctx context.Context, id int) error {
	if !common.RedisEnabled {
		return nil
	}
	quota, err := CacheGetUserQuota(ctx, id)
	if err != nil {
		return err
	}
	err = common.RedisSet(fmt.Sprintf("user_quota:%d", id), fmt.Sprintf("%d", quota), time.Duration(UserId2QuotaCacheSeconds)*time.Second)
	return err
}

func CacheDecreaseUserQuota(id int, quota int64) error {
	if !common.RedisEnabled {
		return nil
	}
	err := common.RedisDecrease(fmt.Sprintf("user_quota:%d", id), int64(quota))
	return err
}

func CacheIsUserEnabled(userId int) (bool, error) {
	if !common.RedisEnabled {
		return IsUserEnabled(userId)
	}
	enabled, err := common.RedisGet(fmt.Sprintf("user_enabled:%d", userId))
	if err == nil {
		return enabled == "1", nil
	}

	userEnabled, err := IsUserEnabled(userId)
	if err != nil {
		return false, err
	}
	enabled = "0"
	if userEnabled {
		enabled = "1"
	}
	err = common.RedisSet(fmt.Sprintf("user_enabled:%d", userId), enabled, time.Duration(UserId2StatusCacheSeconds)*time.Second)
	if err != nil {
		logger.SysError("Redis set user enabled error: " + err.Error())
	}
	return userEnabled, err
}

func CacheGetGroupModels(ctx context.Context, group string) ([]string, error) {
	if !common.RedisEnabled {
		return GetGroupModels(ctx, group)
	}
	modelsStr, err := common.RedisGet(fmt.Sprintf("group_models:%s", group))
	if err == nil {
		return strings.Split(modelsStr, ","), nil
	}
	models, err := GetGroupModels(ctx, group)
	if err != nil {
		return nil, err
	}
	err = common.RedisSet(fmt.Sprintf("group_models:%s", group), strings.Join(models, ","), time.Duration(GroupModelsCacheSeconds)*time.Second)
	if err != nil {
		logger.SysError("Redis set group models error: " + err.Error())
	}
	return models, nil
}

var group2model2channels map[string]map[string][]*Channel
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	newChannelId2channel := make(map[int]*Channel)
	var channels []*Channel
	DB.Where("status = ?", ChannelStatusEnabled).Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]*Channel)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]*Channel)
	}
	for _, channel := range channels {
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]*Channel, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return channels[i].GetPriority() > channels[j].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	channelSyncLock.Unlock()
	logger.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		logger.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func CacheGetRandomSatisfiedChannel(ctx context.Context, group string, model string, ignoreFirstPriority bool, failedIdList []int) (*Channel, error) {
	if !config.MemoryCacheEnabled {
		return GetRandomSatisfiedChannel(group, model, ignoreFirstPriority, failedIdList[0])
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	channels := group2model2channels[group][model]
	if len(channels) == 0 {
		return nil, errors.New("channel not found")
	}
	endIdx := len(channels)

	// 新数组，用于存储提取的结果
	var maxPriority int64 = -10000
	var useableChannels []*Channel
	var chConns map[int][2]int64 = make(map[int][2]int64)
	// 提取原始数组的所有元素到新数组
	for _, channel := range channels {
		// 如果 channel 在 failedIdList 中，则跳过
		// logger.SysLog(fmt.Sprintf("failedIdList: %v", failedIdList))
		if slices.Contains(failedIdList, int(channel.Id)) {
			continue
		}

		var currentConnectionsInt int64 = 0
		modelConnMapping := channel.GetModelConnMapping()
		maxConn := int64(100) // 默认值 100 表示无限制

		// 如果 modelConnMapping 存在且包含目标 model，则获取最大连接数
		if modelConnMapping != nil {
			currentConnections, err := CacheGetChannelCurrentConnections(channel.Id, model)
			if err != nil {
				// logger.SysError(fmt.Sprintf("failed to get channel %d's current connections: %s", channel.Id, err.Error()))
				currentConnections = "0"
			}
			currentConnectionsInt, err = strconv.ParseInt(currentConnections, 10, 64)
			if err != nil {
				// logger.SysError(fmt.Sprintf("failed to parse channel %d's current connections: %s", channel.Id, err.Error()))
				currentConnectionsInt = 0
			}
			if conn, exists := modelConnMapping[model]; exists {
				maxConn = conn
			}
		}

		// 判断当前连接数是否小于最大连接数或无限制
		if maxConn == 100 || currentConnectionsInt < maxConn {
			logger.Debug(ctx, fmt.Sprintf("usable %s channel %d, conn:%d/%d, priority:%d",
				model, channel.Id, currentConnectionsInt, maxConn, channel.GetPriority()))
			chConns[channel.Id] = [2]int64{currentConnectionsInt, maxConn}
			useableChannels = append(useableChannels, channel)
			if channel.GetPriority() > maxPriority {
				maxPriority = channel.GetPriority()
			}
		}
	}

	// 收集所有具有最大优先级的 Channel
	var maxPriorityChannels []*Channel
	for _, ch := range useableChannels {
		if ch.GetPriority() == maxPriority {
			maxPriorityChannels = append(maxPriorityChannels, ch)
		}
	}

	// logger.SysLog(fmt.Sprintf("maxPriorityChannels count: %d %d", maxPriority, len(maxPriorityChannels)))

	// 如果有多个最大优先级的 Channel，随机选择其中一个
	if len(maxPriorityChannels) > 0 {
		randIdx := rand.Intn(len(maxPriorityChannels))
		ch := maxPriorityChannels[randIdx]
		logger.SysLog(fmt.Sprintf("selected %s channel: %d:%s, conn: %d/%d, priority: %d/%d",
			model, ch.Id, ch.Name, chConns[ch.Id][0], chConns[ch.Id][1], ch.GetPriority(), maxPriority))
		return ch, nil
	}

	idx := rand.Intn(endIdx)
	if ignoreFirstPriority {
		if endIdx < len(channels) { // which means there are more than one priority
			idx = random.RandRange(endIdx, len(channels))
		}
	}
	return channels[idx], nil
}

// 在这里添加和减少channel的当前连接数
func CacheGetChannelCurrentConnections(channelId int, model string) (string, error) {
	if !common.RedisEnabled {
		return "0", nil
	}
	return common.RedisGet(fmt.Sprintf("channel_connections:%s:%d", model, channelId))
}

// 在这里添加和减少channel的当前连接数
func CacheIncreaseChannelCurrentConnections(channelId int, model string) error {
	if !common.RedisEnabled {
		return nil
	}
	err := common.RedisIncrease(fmt.Sprintf("channel_connections:%s:%d", model, channelId), 1)
	return err
}

// 在这里添加和减少channel的当前连接数
func CacheDecreaseChannelCurrentConnections(channelId int, model string) error {
	if !common.RedisEnabled {
		return nil
	}
	err := common.RedisDecrease(fmt.Sprintf("channel_connections:%s:%d", model, channelId), 1)
	return err
}
