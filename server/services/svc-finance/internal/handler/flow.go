// Package handler provides HTTP handlers for the finance service.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// FlowItem represents a financial transaction record.
type FlowItem struct {
	ID       string  `json:"id"`
	Amount   float64 `json:"amount"`
	Category string  `json:"category"`
	Time     string  `json:"time"`
	Remark   string  `json:"remark"`
}

// FlowListResponse is the response structure for flow list endpoint.
type FlowListResponse struct {
	Items []FlowItem `json:"items"`
}

// FlowListHandler handles GET /api/finance/flow/list requests.
// When X-Mock header is present, returns fixed mock data for UI integration testing.
func FlowListHandler(c *gin.Context) {
	period := c.Query("period")
	if period == "" {
		period = "2025-10" // default to current month
	}

	// Check if this is a mock request
	isMock := c.GetHeader("X-Mock") == "true"

	if isMock {
		// Return fixed mock data for UI integration testing
		c.JSON(http.StatusOK, FlowListResponse{
			Items: []FlowItem{
				{
					ID:       "mock-001",
					Amount:   -299.50,
					Category: "餐饮",
					Time:     "2025-10-01 12:30",
					Remark:   "午餐费用",
				},
				{
					ID:       "mock-002",
					Amount:   5000.00,
					Category: "工资",
					Time:     "2025-10-01 09:00",
					Remark:   "月度工资收入",
				},
				{
					ID:       "mock-003",
					Amount:   -150.00,
					Category: "交通",
					Time:     "2025-10-02 08:15",
					Remark:   "地铁充值",
				},
				{
					ID:       "mock-004",
					Amount:   -89.90,
					Category: "购物",
					Time:     "2025-10-03 15:45",
					Remark:   "日用品采购",
				},
				{
					ID:       "mock-005",
					Amount:   200.00,
					Category: "红包",
					Time:     "2025-10-04 20:00",
					Remark:   "生日红包",
				},
			},
		})
		return
	}

	// Real implementation would query database here
	// For now, return empty list when not in mock mode
	c.JSON(http.StatusOK, FlowListResponse{
		Items: []FlowItem{},
	})
}
