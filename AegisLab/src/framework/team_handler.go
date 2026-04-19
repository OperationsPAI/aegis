package framework

import "github.com/gin-gonic/gin"

type TeamRoutesHandler interface {
	CreateTeam(*gin.Context)
	DeleteTeam(*gin.Context)
	GetTeamDetail(*gin.Context)
	ListTeams(*gin.Context)
	UpdateTeam(*gin.Context)
	ListTeamProjects(*gin.Context)
	AddTeamMember(*gin.Context)
	RemoveTeamMember(*gin.Context)
	UpdateTeamMemberRole(*gin.Context)
	ListTeamMembers(*gin.Context)
}
