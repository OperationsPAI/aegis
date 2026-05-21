package handler

import (
	"aegis/internal/chaosengine/systemconfig"
)

type SystemType systemconfig.SystemType

const (
	SystemTrainTicket        = SystemType(systemconfig.SystemTrainTicket)
	SystemOtelDemo           = SystemType(systemconfig.SystemOtelDemo)
	SystemMediaMicroservices = SystemType(systemconfig.SystemMediaMicroservices)
	SystemHotelReservation   = SystemType(systemconfig.SystemHotelReservation)
	SystemSocialNetwork      = SystemType(systemconfig.SystemSocialNetwork)
	SystemOnlineBoutique     = SystemType(systemconfig.SystemOnlineBoutique)
	SystemSockShop           = SystemType(systemconfig.SystemSockShop)
	SystemTeaStore           = SystemType(systemconfig.SystemTeaStore)
)

func (s SystemType) String() string {
	return systemconfig.SystemType(s).String()
}

func (s SystemType) IsValid() bool {
	_, err := systemconfig.ParseSystemType(s.String())
	return err == nil
}

func GetAllSystemTypes() []SystemType {
	systems := systemconfig.GetAllSystemTypes()
	result := make([]SystemType, len(systems))
	for i, sys := range systems {
		result[i] = SystemType(sys)
	}
	return result
}

type InjectionConf struct{}
