package system

import (
	"aegis/clients/sso"
	"aegis/platform/dto"
	"aegis/platform/model"
)

func buildAuditLogListResp(logs []model.AuditLog, req *ListAuditLogReq, total int64, users map[int]*ssoclient.UserInfo) *dto.ListResp[AuditLogResp] {
	logResps := make([]AuditLogResp, 0, len(logs))
	for i := range logs {
		logResps = append(logResps, *NewAuditLogResp(&logs[i], users))
	}

	return &dto.ListResp[AuditLogResp]{
		Items:      logResps,
		Pagination: req.ConvertToPaginationInfo(total),
	}
}

func buildConfigDetailResp(cfg *model.DynamicConfig, histories []model.ConfigHistory, users map[int]*ssoclient.UserInfo) *ConfigDetailResp {
	resp := NewConfigDetailResp(cfg, users)
	for _, history := range histories {
		resp.Histories = append(resp.Histories, *NewConfigHistoryResp(&history, users))
	}
	return resp
}

func buildConfigListResp(configs []model.DynamicConfig, req *ListConfigReq, total int64, users map[int]*ssoclient.UserInfo) *dto.ListResp[ConfigResp] {
	configResps := make([]ConfigResp, 0, len(configs))
	for i := range configs {
		configResps = append(configResps, *NewConfigResp(&configs[i], users))
	}

	return &dto.ListResp[ConfigResp]{
		Items:      configResps,
		Pagination: req.ConvertToPaginationInfo(total),
	}
}

func buildConfigHistoryListResp(histories []model.ConfigHistory, req *ListConfigHistoryReq, total int64, users map[int]*ssoclient.UserInfo) *dto.ListResp[ConfigHistoryResp] {
	historyResps := make([]ConfigHistoryResp, 0, len(histories))
	for i := range histories {
		historyResps = append(historyResps, *NewConfigHistoryResp(&histories[i], users))
	}

	return &dto.ListResp[ConfigHistoryResp]{
		Items:      historyResps,
		Pagination: req.ConvertToPaginationInfo(total),
	}
}
