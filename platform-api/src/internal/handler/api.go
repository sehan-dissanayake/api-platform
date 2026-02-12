/*
 *  Copyright (c) 2025, WSO2 LLC. (http://www.wso2.org) All Rights Reserved.
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 *
 */

package handler

import (
	"encoding/json"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"platform-api/src/api"
	"platform-api/src/internal/constants"
	"platform-api/src/internal/dto"
	"platform-api/src/internal/middleware"
	"platform-api/src/internal/service"
	"platform-api/src/internal/utils"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type APIHandler struct {
	apiService *service.APIService
}

func NewAPIHandler(apiService *service.APIService) *APIHandler {
	return &APIHandler{
		apiService: apiService,
	}
}

// CreateAPI handles POST /api/v1/rest-apis and creates a new API
func (h *APIHandler) CreateAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	var req api.CreateRESTAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.NewValidationErrorResponse(c, err)
		return
	}

	// Validate required fields
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API name is required"))
		return
	}
	if req.Context == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API context is required"))
		return
	}
	if req.Version == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API version is required"))
		return
	}
	if req.ProjectId == (openapi_types.UUID{}) {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Project ID is required"))
		return
	}
	if isEmptyUpstreamDefinition(req.Upstream.Main) && (req.Upstream.Sandbox == nil || isEmptyUpstreamDefinition(*req.Upstream.Sandbox)) {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"At least one upstream endpoint (main or sandbox) is required"))
		return
	}

	apiResponse, err := h.apiService.CreateAPI(&req, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrHandleExists) {
			c.JSON(http.StatusConflict, utils.NewErrorResponse(409, "Conflict",
				"API handle already exists"))
			return
		}
		if errors.Is(err, constants.ErrAPINameVersionAlreadyExists) {
			c.JSON(http.StatusConflict, utils.NewErrorResponse(409, "Conflict",
				"API with same name and version already exists in the organization"))
			return
		}
		if errors.Is(err, constants.ErrAPIAlreadyExists) {
			c.JSON(http.StatusConflict, utils.NewErrorResponse(409, "Conflict",
				"API already exists in the project"))
			return
		}
		if errors.Is(err, constants.ErrProjectNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"Project not found"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIName) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API name format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIContext) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API context format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIVersion) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API version format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidLifecycleState) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid lifecycle status"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIType) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API type"))
			return
		}
		if errors.Is(err, constants.ErrInvalidTransport) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid transport protocol"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to create API"))
		return
	}

	c.JSON(http.StatusCreated, apiResponse)
}

// GetAPI handles GET /api/v1/rest-apis/:apiId and retrieves an API by its handle
func (h *APIHandler) GetAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	apiId := c.Param("apiId")
	if apiId == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	apiResponse, err := h.apiService.GetAPIByHandle(apiId, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrAPINotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"API not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to get API"))
		return
	}

	c.JSON(http.StatusOK, apiResponse)
}

// ListAPIs handles GET /api/v1/rest-apis and lists APIs for an organization with optional project filter
func (h *APIHandler) ListAPIs(c *gin.Context) {
	// Get organization from JWT token
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	var params api.ListRESTAPIsParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request", err.Error()))
		return
	}

	var projectId string
	if params.ProjectId != nil {
		projectId = string(*params.ProjectId)
	}

	apis, err := h.apiService.GetAPIsByOrganization(orgId, projectId)
	if err != nil {
		if errors.Is(err, constants.ErrProjectNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"Project not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to get APIs"))
		return
	}

	response := api.RESTAPIListResponse{
		Count: len(apis),
		List:  apis,
		Pagination: api.Pagination{
			Total:  len(apis),
			Offset: 0,
			Limit:  len(apis),
		},
	}

	c.JSON(http.StatusOK, response)
}

// UpdateAPI updates an existing API identified by handle
func (h *APIHandler) UpdateAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	apiId := c.Param("apiId")
	if apiId == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	var req api.UpdateRESTAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			err.Error()))
		return
	}

	// Validate upstream configuration if provided
	if isEmptyUpstreamDefinition(req.Upstream.Main) && (req.Upstream.Sandbox == nil || isEmptyUpstreamDefinition(*req.Upstream.Sandbox)) {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"At least one upstream endpoint (main or sandbox) is required"))
		return
	}

	apiResponse, err := h.apiService.UpdateAPIByHandle(apiId, &req, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrAPINotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"API not found"))
			return
		}
		if errors.Is(err, constants.ErrInvalidLifecycleState) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid lifecycle status"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIType) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API type"))
			return
		}
		if errors.Is(err, constants.ErrInvalidTransport) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid transport protocol"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to update API"))
		return
	}

	c.JSON(http.StatusOK, apiResponse)
}

// DeleteAPI handles DELETE /api/v1/rest-apis/:apiId and deletes an API by its handle
func (h *APIHandler) DeleteAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	apiId := c.Param("apiId")
	if apiId == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	err := h.apiService.DeleteAPIByHandle(apiId, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrAPINotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"API not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to delete API"))
		return
	}

	c.JSON(http.StatusNoContent, nil)
}

// AddGatewaysToAPI handles POST /api/v1/rest-apis/:apiId/gateways to associate gateways with an API
func (h *APIHandler) AddGatewaysToAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	apiId := c.Param("apiId")
	if apiId == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	var req []api.AddGatewayToRESTAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request", err.Error()))
		return
	}

	if len(req) == 0 {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"At least one gateway ID is required"))
		return
	}

	// Extract gateway IDs from request
	gatewayIds := make([]string, len(req))
	for i, gw := range req {
		gatewayIds[i] = utils.OpenAPIUUIDToString(gw.GatewayId)
	}

	gatewaysResponse, err := h.apiService.AddGatewaysToAPIByHandle(apiId, gatewayIds, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrAPINotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"API not found"))
			return
		}
		if errors.Is(err, constants.ErrGatewayNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"One or more gateways not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to associate gateways with API"))
		return
	}

	c.JSON(http.StatusOK, gatewaysResponse)
}

// GetAPIGateways handles GET /api/v1/rest-apis/:apiId/gateways to get gateways associated with an API including deployment details
func (h *APIHandler) GetAPIGateways(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	apiId := c.Param("apiId")
	if apiId == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	gatewaysResponse, err := h.apiService.GetAPIGatewaysByHandle(apiId, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrAPINotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"API not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to get API gateways"))
		return
	}

	c.JSON(http.StatusOK, gatewaysResponse)
}

// PublishToDevPortal handles POST /api/v1/rest-apis/:apiId/devportals/publish
//
// This endpoint publishes an API to a specific DevPortal with its metadata and OpenAPI definition.
// The API must exist in platform-api and the specified DevPortal must be active.
func (h *APIHandler) PublishToDevPortal(c *gin.Context) {
	// Extract organization ID from context
	orgID, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	// Extract and validate apiId path parameter
	apiID := c.Param("apiId")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	// Parse request body
	var req api.PublishToDevPortalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		status, errorResp := utils.GetErrorResponse(err)
		c.JSON(status, errorResp)
		return
	}

	// Publish API to DevPortal through service layer
	err := h.apiService.PublishAPIToDevPortalByHandle(apiID, &req, orgID)
	if err != nil {
		status, errorResp := utils.GetErrorResponse(err)
		c.JSON(status, errorResp)
		return
	}

	// Log successful publish
	log.Printf("[APIHandler] API %s published successfully to DevPortal %s", apiID, utils.OpenAPIUUIDToString(req.DevPortalUuid))

	// Return success response
	c.JSON(http.StatusOK, api.CommonResponse{
		Success:   true,
		Message:   "API published successfully to DevPortal",
		Timestamp: time.Now(),
	})
}

// UnpublishFromDevPortal handles POST /api/v1/rest-apis/:apiId/devportals/unpublish
//
// This endpoint unpublishes an API from a specific DevPortal by deleting it.
// The API must exist in platform-api and the specified DevPortal must exist.
func (h *APIHandler) UnpublishFromDevPortal(c *gin.Context) {
	// Extract organization ID from context
	orgID, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	// Extract and validate apiId path parameter
	apiID := c.Param("apiId")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}

	// Parse request body
	var req api.UnpublishFromDevPortalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		status, errorResp := utils.GetErrorResponse(err)
		c.JSON(status, errorResp)
		return
	}

	// Unpublish API from DevPortal through service layer
	err := h.apiService.UnpublishAPIFromDevPortalByHandle(apiID, utils.OpenAPIUUIDToString(req.DevPortalUuid), orgID)
	if err != nil {
		status, errorResp := utils.GetErrorResponse(err)
		c.JSON(status, errorResp)
		return
	}

	// Log successful unpublish
	log.Printf("[APIHandler] API %s unpublished successfully from DevPortal %s", apiID, utils.OpenAPIUUIDToString(req.DevPortalUuid))

	// Return success response
	c.JSON(http.StatusOK, api.CommonResponse{
		Success:   true,
		Message:   "API unpublished successfully from DevPortal",
		Timestamp: time.Now(),
	})
}

// GetAPIPublications handles GET /api/v1/rest-apis/:apiId/publications
//
// This endpoint retrieves all DevPortals associated with an API including publication details.
func (h *APIHandler) GetAPIPublications(c *gin.Context) {
	// Extract organization ID from context
	orgID, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	// Extract and validate apiId path parameter
	apiID := c.Param("apiId")
	if apiID == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API ID is required"))
		return
	}
	// Get publications through service layer
	response, err := h.apiService.GetAPIPublicationsByHandle(apiID, orgID)
	if err != nil {
		// Handle specific errors
		if errors.Is(err, constants.ErrAPINotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"API not found"))
			return
		}

		// Internal server error
		log.Printf("[APIHandler] Failed to get publications for API %s: %v", apiID, err)
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to retrieve API publications"))
		return
	}

	// Return success response
	c.JSON(http.StatusOK, response)
}

// ImportAPIProject handles POST /api/v1/import/api-project
func (h *APIHandler) ImportAPIProject(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	var req api.ImportAPIProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request", err.Error()))
		return
	}

	// Validate required fields
	if req.RepoUrl == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Repository URL is required"))
		return
	}
	if req.Branch == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Branch is required"))
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Path is required"))
		return
	}
	if req.Api.Name == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API name is required"))
		return
	}
	if req.Api.Context == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API context is required"))
		return
	}
	if req.Api.Version == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API version is required"))
		return
	}
	if req.Api.ProjectId == (openapi_types.UUID{}) {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Project ID is required"))
		return
	}

	serviceReq, err := toServiceImportAPIProjectRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Invalid API project request"))
		return
	}

	// Create Git service
	gitService := service.NewGitService()

	// Import API project
	apiResponse, err := h.apiService.ImportAPIProject(&serviceReq, orgId, gitService)
	if err != nil {
		if errors.Is(err, constants.ErrAPIProjectNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "API Project Not Found",
				"API project not found: .api-platform directory not found"))
			return
		}
		if errors.Is(err, constants.ErrMalformedAPIProject) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Malformed API Project",
				"Malformed API project: config.yaml is missing or invalid"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIProject) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Invalid API Project",
				"Invalid API project: referenced files not found"))
			return
		}
		if errors.Is(err, constants.ErrConfigFileNotFound) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Config File Not Found",
				"config.yaml file not found in .api-platform directory"))
			return
		}
		if errors.Is(err, constants.ErrOpenAPIFileNotFound) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "OpenAPI File Not Found",
				"OpenAPI file not found"))
			return
		}
		if errors.Is(err, constants.ErrWSO2ArtifactNotFound) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "WSO2 Artifact Not Found",
				"WSO2 artifact file not found"))
			return
		}
		if errors.Is(err, constants.ErrAPIAlreadyExists) {
			c.JSON(http.StatusConflict, utils.NewErrorResponse(409, "Conflict",
				"API already exists in the project"))
			return
		}
		if errors.Is(err, constants.ErrProjectNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"Project not found"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIName) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API name format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIContext) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API context format"))
			return
		}

		log.Printf("Failed to import API project: %v", err)
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to import API project"))
		return
	}

	c.JSON(http.StatusCreated, apiResponse)
}

// ValidateAPIProject handles POST /validate/api-project
func (h *APIHandler) ValidateAPIProject(c *gin.Context) {
	var req api.ValidateAPIProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request", err.Error()))
		return
	}

	// Validate required fields
	if req.RepoUrl == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Repository URL is required"))
		return
	}
	if req.Branch == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Branch is required"))
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Path is required"))
		return
	}

	serviceReq := toServiceValidateAPIProjectRequest(&req)

	// Create Git service
	gitService := service.NewGitService()

	// Validate API project
	responseDTO, err := h.apiService.ValidateAndRetrieveAPIProject(&serviceReq, gitService)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to validate API project"))
		return
	}

	response, err := toRESTAPIProjectValidationResponse(responseDTO)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to validate API project"))
		return
	}

	// Return validation response (200 OK even if validation fails - errors are in the response body)
	c.JSON(http.StatusOK, response)
}

// ValidateOpenAPI handles POST /validate/open-api
func (h *APIHandler) ValidateOpenAPI(c *gin.Context) {
	// Parse multipart form
	err := c.Request.ParseMultipartForm(10 << 20) // 10 MB max
	if err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Failed to parse multipart form"))
		return
	}

	var req api.ValidateOpenAPIRequest
	var definitionHeader *multipart.FileHeader

	// Get URL from form if provided
	if url := c.PostForm("url"); url != "" {
		req.Url = &url
	}

	// Get definition file from form if provided
	if file, header, err := c.Request.FormFile("definition"); err == nil {
		definitionHeader = header
		var openapiFile openapi_types.File
		openapiFile.InitFromMultipart(header)
		req.Definition = &openapiFile
		defer file.Close()
	}

	// Validate that at least one input is provided
	if req.Url == nil && req.Definition == nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Either URL or definition file must be provided"))
		return
	}

	serviceReq := dto.ValidateOpenAPIRequest{}
	if req.Url != nil {
		serviceReq.URL = *req.Url
	}
	serviceReq.Definition = definitionHeader

	// Validate OpenAPI definition
	responseDTO, err := h.apiService.ValidateOpenAPIDefinition(&serviceReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to validate OpenAPI definition"))
		return
	}

	response, err := toOpenAPIValidationResponse(responseDTO)
	if err != nil {
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to validate OpenAPI definition"))
		return
	}

	// Return validation response (200 OK even if validation fails - errors are in the response body)
	c.JSON(http.StatusOK, response)
}

// ImportOpenAPI handles POST /import/open-api and imports an API from OpenAPI definition
func (h *APIHandler) ImportOpenAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	// Parse multipart form
	err := c.Request.ParseMultipartForm(10 << 20) // 10 MB max
	if err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Failed to parse multipart form"))
		return
	}

	var req api.ImportOpenAPIRequest
	var definitionHeader *multipart.FileHeader

	// Get URL from form if provided
	if url := c.PostForm("url"); url != "" {
		req.Url = &url
	}

	// Get definition file from form if provided
	if file, header, err := c.Request.FormFile("definition"); err == nil {
		definitionHeader = header
		var openapiFile openapi_types.File
		openapiFile.InitFromMultipart(header)
		req.Definition = &openapiFile
		defer file.Close()
	}

	// Validate that at least one input is provided
	if req.Url == nil && req.Definition == nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Either URL or definition file must be provided"))
		return
	}

	// Get API details from form data (JSON string in 'api' field)
	apiJSON := c.PostForm("api")
	if apiJSON == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API details are required"))
		return
	}
	req.Api = apiJSON

	// Parse API details from JSON string
	var apiDetails api.RESTAPI
	if err := json.Unmarshal([]byte(apiJSON), &apiDetails); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Invalid API details: "+err.Error()))
		return
	}

	if apiDetails.Name == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API name is required"))
		return
	}
	if apiDetails.Context == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API context is required"))
		return
	}
	if apiDetails.Version == "" {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"API version is required"))
		return
	}
	if apiDetails.ProjectId == (openapi_types.UUID{}) {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Project ID is required"))
		return
	}

	serviceReq, err := toServiceImportOpenAPIRequest(&req, &apiDetails, definitionHeader)
	if err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Invalid API details"))
		return
	}

	// Import API from OpenAPI definition
	apiResponse, err := h.apiService.ImportFromOpenAPI(serviceReq, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrAPIAlreadyExists) {
			c.JSON(http.StatusConflict, utils.NewErrorResponse(409, "Conflict",
				"API already exists in the project"))
			return
		}
		if errors.Is(err, constants.ErrProjectNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"Project not found"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIName) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API name format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIContext) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API context format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIVersion) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API version format"))
			return
		}
		if errors.Is(err, constants.ErrInvalidLifecycleState) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid lifecycle status"))
			return
		}
		if errors.Is(err, constants.ErrInvalidAPIType) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid API type"))
			return
		}
		if errors.Is(err, constants.ErrInvalidTransport) {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid transport protocol"))
			return
		}
		// Handle OpenAPI-specific errors
		if strings.Contains(err.Error(), "failed to fetch OpenAPI from URL") {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Failed to fetch OpenAPI definition from URL"))
			return
		}
		if strings.Contains(err.Error(), "failed to open OpenAPI definition file") ||
			strings.Contains(err.Error(), "failed to read OpenAPI definition file") {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Failed to fetch OpenAPI definition from file"))
			return
		}
		if strings.Contains(err.Error(), "failed to validate and parse OpenAPI definition") {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Invalid OpenAPI definition"))
			return
		}
		if strings.Contains(err.Error(), "failed to merge API details") {
			c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
				"Failed to create API from OpenAPI definition: incompatible details"))
			return
		}

		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to import API from OpenAPI definition"))
		return
	}

	c.JSON(http.StatusCreated, apiResponse)
}

// ValidateAPI handles GET /api/v1/rest-apis/validate
func (h *APIHandler) ValidateAPI(c *gin.Context) {
	orgId, exists := middleware.GetOrganizationFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, utils.NewErrorResponse(401, "Unauthorized",
			"Organization claim not found in token"))
		return
	}

	var params api.ValidateRESTAPIParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request", err.Error()))
		return
	}

	var req dto.APIValidationRequest
	if params.Identifier != nil {
		req.Identifier = string(*params.Identifier)
	}
	if params.Name != nil {
		req.Name = string(*params.Name)
	}
	if params.Version != nil {
		req.Version = string(*params.Version)
	}

	// Validate that either identifier OR both name and version are provided
	if req.Identifier == "" && (req.Name == "" || req.Version == "") {
		c.JSON(http.StatusBadRequest, utils.NewErrorResponse(400, "Bad Request",
			"Either 'identifier' or both 'name' and 'version' query parameters are required"))
		return
	}

	responseDTO, err := h.apiService.ValidateAPI(&req, orgId)
	if err != nil {
		if errors.Is(err, constants.ErrOrganizationNotFound) {
			c.JSON(http.StatusNotFound, utils.NewErrorResponse(404, "Not Found",
				"Organization not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, utils.NewErrorResponse(500, "Internal Server Error",
			"Failed to validate API"))
		return
	}

	// Always return 200 OK with the validation result
	c.JSON(http.StatusOK, toRESTAPIValidationResponse(responseDTO))
}

// RegisterRoutes registers all API routes
func (h *APIHandler) RegisterRoutes(r *gin.Engine) {
	// API routes
	apiGroup := r.Group("/api/v1/rest-apis")
	{
		apiGroup.POST("", h.CreateAPI)
		apiGroup.GET("", h.ListAPIs)
		apiGroup.GET("/:apiId", h.GetAPI)
		apiGroup.PUT("/:apiId", h.UpdateAPI)
		apiGroup.DELETE("/:apiId", h.DeleteAPI)
		apiGroup.GET("/validate", h.ValidateAPI)
		apiGroup.GET("/:apiId/gateways", h.GetAPIGateways)
		apiGroup.POST("/:apiId/gateways", h.AddGatewaysToAPI)
		apiGroup.POST("/:apiId/devportals/publish", h.PublishToDevPortal)
		apiGroup.POST("/:apiId/devportals/unpublish", h.UnpublishFromDevPortal)
		apiGroup.GET("/:apiId/publications", h.GetAPIPublications)
	}
	importGroup := r.Group("/api/v1/import")
	{
		importGroup.POST("/api-project", h.ImportAPIProject)
		importGroup.POST("/open-api", h.ImportOpenAPI)
	}
	validateGroup := r.Group("/api/v1/validate")
	{
		validateGroup.POST("/api-project", h.ValidateAPIProject)
		validateGroup.POST("/open-api", h.ValidateOpenAPI)
	}
}

func toServiceImportAPIProjectRequest(req *api.ImportAPIProjectRequest) (dto.ImportAPIProjectRequest, error) {
	apiDTO, err := toDTOAPIFromImportRequest(req)
	if err != nil {
		return dto.ImportAPIProjectRequest{}, err
	}
	provider := ""
	if req.Provider != nil {
		provider = string(*req.Provider)
	}
	return dto.ImportAPIProjectRequest{
		RepoURL:  req.RepoUrl,
		Provider: provider,
		Branch:   req.Branch,
		Path:     req.Path,
		API:      *apiDTO,
	}, nil
}

func toServiceValidateAPIProjectRequest(req *api.ValidateAPIProjectRequest) dto.ValidateAPIProjectRequest {
	provider := ""
	if req.Provider != nil {
		provider = string(*req.Provider)
	}
	return dto.ValidateAPIProjectRequest{
		RepoURL:  req.RepoUrl,
		Provider: provider,
		Branch:   req.Branch,
		Path:     req.Path,
	}
}

func toServiceImportOpenAPIRequest(req *api.ImportOpenAPIRequest, apiDetails *api.RESTAPI,
	definitionHeader *multipart.FileHeader) (*dto.ImportOpenAPIRequest, error) {
	apiDTO, err := toDTOAPIFromRESTAPI(apiDetails)
	if err != nil {
		return nil, err
	}
	serviceReq := &dto.ImportOpenAPIRequest{
		API:        *apiDTO,
		Definition: definitionHeader,
	}
	if req.Url != nil {
		serviceReq.URL = *req.Url
	}
	return serviceReq, nil
}

func toRESTAPI(dtoAPI *dto.API) (*api.RESTAPI, error) {
	if dtoAPI == nil {
		return nil, nil
	}

	projectID, err := utils.ParseOpenAPIUUID(dtoAPI.ProjectID)
	if err != nil {
		return nil, err
	}

	response := &api.RESTAPI{
		Channels:        toAPIChannelsPtr(dtoAPI.Channels),
		Context:         dtoAPI.Context,
		CreatedAt:       utils.TimePtrIfNotZero(dtoAPI.CreatedAt),
		CreatedBy:       utils.StringPtrIfNotEmpty(dtoAPI.CreatedBy),
		Description:     utils.StringPtrIfNotEmpty(dtoAPI.Description),
		Id:              utils.StringPtrIfNotEmpty(dtoAPI.ID),
		Kind:            utils.StringPtrIfNotEmpty(dtoAPI.Kind),
		LifeCycleStatus: restAPILifeCycleStatusPtr(dtoAPI.LifeCycleStatus),
		Name:            dtoAPI.Name,
		Operations:      toAPIOperationsPtr(dtoAPI.Operations),
		Policies:        toAPIPoliciesPtr(dtoAPI.Policies),
		ProjectId:       *projectID,
		Transport:       stringSlicePtr(dtoAPI.Transport),
		UpdatedAt:       utils.TimePtrIfNotZero(dtoAPI.UpdatedAt),
		Upstream:        toAPIUpstream(dtoAPI.Upstream),
		Version:         dtoAPI.Version,
	}

	return response, nil
}

func toRESTAPIListResponse(apis []*dto.API) (*api.RESTAPIListResponse, error) {
	list := make([]api.RESTAPI, 0, len(apis))
	for _, item := range apis {
		apiItem, err := toRESTAPI(item)
		if err != nil {
			return nil, err
		}
		if apiItem != nil {
			list = append(list, *apiItem)
		}
	}

	return &api.RESTAPIListResponse{
		Count: len(list),
		List:  list,
		Pagination: api.Pagination{
			Total:  len(list),
			Offset: 0,
			Limit:  len(list),
		},
	}, nil
}

func toRESTAPIProjectValidationResponse(dtoResponse *dto.APIProjectValidationResponse) (*api.RESTAPIProjectValidationResponse, error) {
	if dtoResponse == nil {
		return &api.RESTAPIProjectValidationResponse{}, nil
	}

	apiData, err := toRESTAPIProjectValidationAPI(dtoResponse.API)
	if err != nil {
		return nil, err
	}

	return &api.RESTAPIProjectValidationResponse{
		Api:                      apiData,
		Errors:                   stringSlicePtr(dtoResponse.Errors),
		IsAPIConfigValid:         dtoResponse.IsAPIConfigValid,
		IsRESTAPIDefinitionValid: dtoResponse.IsAPIDefinitionValid,
		IsRESTAPIProjectValid:    dtoResponse.IsAPIProjectValid,
	}, nil
}

func toOpenAPIValidationResponse(dtoResponse *dto.OpenAPIValidationResponse) (*api.OpenAPIValidationResponse, error) {
	if dtoResponse == nil {
		return &api.OpenAPIValidationResponse{}, nil
	}

	apiData, err := toOpenAPIValidationAPI(dtoResponse.API)
	if err != nil {
		return nil, err
	}

	return &api.OpenAPIValidationResponse{
		Api:                      apiData,
		Errors:                   stringSlicePtr(dtoResponse.Errors),
		IsRESTAPIDefinitionValid: dtoResponse.IsAPIDefinitionValid,
	}, nil
}

func toRESTAPIValidationResponse(dtoResponse *dto.APIValidationResponse) *api.RESTAPIValidationResponse {
	if dtoResponse == nil {
		return &api.RESTAPIValidationResponse{Valid: false}
	}

	var errDetails *struct {
		Code    string `json:"code" yaml:"code"`
		Message string `json:"message" yaml:"message"`
	}
	if dtoResponse.Error != nil {
		errDetails = &struct {
			Code    string `json:"code" yaml:"code"`
			Message string `json:"message" yaml:"message"`
		}{
			Code:    dtoResponse.Error.Code,
			Message: dtoResponse.Error.Message,
		}
	}

	return &api.RESTAPIValidationResponse{
		Error: errDetails,
		Valid: dtoResponse.Valid,
	}
}

func toRESTAPIProjectValidationAPI(dtoAPI *dto.API) (*struct {
	Channels        *[]api.Channel                                          `json:"channels,omitempty" yaml:"channels,omitempty"`
	Context         string                                                  `binding:"required" json:"context" yaml:"context"`
	CreatedAt       *time.Time                                              `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	CreatedBy       *string                                                 `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	Description     string                                                  `json:"description" yaml:"description"`
	Id              *string                                                 `json:"id,omitempty" yaml:"id,omitempty"`
	Kind            *string                                                 `json:"kind,omitempty" yaml:"kind,omitempty"`
	LifeCycleStatus *api.RESTAPIProjectValidationResponseApiLifeCycleStatus `json:"lifeCycleStatus,omitempty" yaml:"lifeCycleStatus,omitempty"`
	Name            string                                                  `binding:"required" json:"name" yaml:"name"`
	Operations      []api.Operation                                         `json:"operations" yaml:"operations"`
	Policies        *[]api.Policy                                           `json:"policies,omitempty" yaml:"policies,omitempty"`
	ProjectId       openapi_types.UUID                                      `binding:"required" json:"projectId" yaml:"projectId"`
	Transport       *[]string                                               `json:"transport,omitempty" yaml:"transport,omitempty"`
	UpdatedAt       *time.Time                                              `json:"updatedAt,omitempty" yaml:"updatedAt,omitempty"`
	Upstream        api.Upstream                                            `json:"upstream" yaml:"upstream"`
	Version         string                                                  `binding:"required" json:"version" yaml:"version"`
}, error) {
	if dtoAPI == nil {
		return nil, nil
	}

	restAPI, err := toRESTAPI(dtoAPI)
	if err != nil || restAPI == nil {
		return nil, err
	}

	var status *api.RESTAPIProjectValidationResponseApiLifeCycleStatus
	if restAPI.LifeCycleStatus != nil {
		value := api.RESTAPIProjectValidationResponseApiLifeCycleStatus(*restAPI.LifeCycleStatus)
		status = &value
	}

	operations := []api.Operation{}
	if restAPI.Operations != nil {
		operations = *restAPI.Operations
	}

	return &struct {
		Channels        *[]api.Channel                                          `json:"channels,omitempty" yaml:"channels,omitempty"`
		Context         string                                                  `binding:"required" json:"context" yaml:"context"`
		CreatedAt       *time.Time                                              `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
		CreatedBy       *string                                                 `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
		Description     string                                                  `json:"description" yaml:"description"`
		Id              *string                                                 `json:"id,omitempty" yaml:"id,omitempty"`
		Kind            *string                                                 `json:"kind,omitempty" yaml:"kind,omitempty"`
		LifeCycleStatus *api.RESTAPIProjectValidationResponseApiLifeCycleStatus `json:"lifeCycleStatus,omitempty" yaml:"lifeCycleStatus,omitempty"`
		Name            string                                                  `binding:"required" json:"name" yaml:"name"`
		Operations      []api.Operation                                         `json:"operations" yaml:"operations"`
		Policies        *[]api.Policy                                           `json:"policies,omitempty" yaml:"policies,omitempty"`
		ProjectId       openapi_types.UUID                                      `binding:"required" json:"projectId" yaml:"projectId"`
		Transport       *[]string                                               `json:"transport,omitempty" yaml:"transport,omitempty"`
		UpdatedAt       *time.Time                                              `json:"updatedAt,omitempty" yaml:"updatedAt,omitempty"`
		Upstream        api.Upstream                                            `json:"upstream" yaml:"upstream"`
		Version         string                                                  `binding:"required" json:"version" yaml:"version"`
	}{
		Channels:        restAPI.Channels,
		Context:         restAPI.Context,
		CreatedAt:       restAPI.CreatedAt,
		CreatedBy:       restAPI.CreatedBy,
		Description:     stringValue(restAPI.Description),
		Id:              restAPI.Id,
		Kind:            restAPI.Kind,
		LifeCycleStatus: status,
		Name:            restAPI.Name,
		Operations:      operations,
		Policies:        restAPI.Policies,
		ProjectId:       restAPI.ProjectId,
		Transport:       restAPI.Transport,
		UpdatedAt:       restAPI.UpdatedAt,
		Upstream:        restAPI.Upstream,
		Version:         restAPI.Version,
	}, nil
}

func toOpenAPIValidationAPI(dtoAPI *dto.API) (*struct {
	Channels        *[]api.Channel                                   `json:"channels,omitempty" yaml:"channels,omitempty"`
	Context         string                                           `binding:"required" json:"context" yaml:"context"`
	CreatedAt       *time.Time                                       `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	CreatedBy       *string                                          `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	Description     *string                                          `json:"description,omitempty" yaml:"description,omitempty"`
	Id              *string                                          `json:"id,omitempty" yaml:"id,omitempty"`
	Kind            *string                                          `json:"kind,omitempty" yaml:"kind,omitempty"`
	LifeCycleStatus *api.OpenAPIValidationResponseApiLifeCycleStatus `json:"lifeCycleStatus,omitempty" yaml:"lifeCycleStatus,omitempty"`
	Name            string                                           `binding:"required" json:"name" yaml:"name"`
	Operations      []api.Operation                                  `json:"operations" yaml:"operations"`
	Policies        *[]api.Policy                                    `json:"policies,omitempty" yaml:"policies,omitempty"`
	ProjectId       openapi_types.UUID                               `binding:"required" json:"projectId" yaml:"projectId"`
	Transport       *[]string                                        `json:"transport,omitempty" yaml:"transport,omitempty"`
	UpdatedAt       *time.Time                                       `json:"updatedAt,omitempty" yaml:"updatedAt,omitempty"`
	Upstream        api.Upstream                                     `json:"upstream" yaml:"upstream"`
	Version         string                                           `binding:"required" json:"version" yaml:"version"`
}, error) {
	if dtoAPI == nil {
		return nil, nil
	}

	restAPI, err := toRESTAPI(dtoAPI)
	if err != nil || restAPI == nil {
		return nil, err
	}

	var status *api.OpenAPIValidationResponseApiLifeCycleStatus
	if restAPI.LifeCycleStatus != nil {
		value := api.OpenAPIValidationResponseApiLifeCycleStatus(*restAPI.LifeCycleStatus)
		status = &value
	}

	operations := []api.Operation{}
	if restAPI.Operations != nil {
		operations = *restAPI.Operations
	}

	return &struct {
		Channels        *[]api.Channel                                   `json:"channels,omitempty" yaml:"channels,omitempty"`
		Context         string                                           `binding:"required" json:"context" yaml:"context"`
		CreatedAt       *time.Time                                       `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
		CreatedBy       *string                                          `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
		Description     *string                                          `json:"description,omitempty" yaml:"description,omitempty"`
		Id              *string                                          `json:"id,omitempty" yaml:"id,omitempty"`
		Kind            *string                                          `json:"kind,omitempty" yaml:"kind,omitempty"`
		LifeCycleStatus *api.OpenAPIValidationResponseApiLifeCycleStatus `json:"lifeCycleStatus,omitempty" yaml:"lifeCycleStatus,omitempty"`
		Name            string                                           `binding:"required" json:"name" yaml:"name"`
		Operations      []api.Operation                                  `json:"operations" yaml:"operations"`
		Policies        *[]api.Policy                                    `json:"policies,omitempty" yaml:"policies,omitempty"`
		ProjectId       openapi_types.UUID                               `binding:"required" json:"projectId" yaml:"projectId"`
		Transport       *[]string                                        `json:"transport,omitempty" yaml:"transport,omitempty"`
		UpdatedAt       *time.Time                                       `json:"updatedAt,omitempty" yaml:"updatedAt,omitempty"`
		Upstream        api.Upstream                                     `json:"upstream" yaml:"upstream"`
		Version         string                                           `binding:"required" json:"version" yaml:"version"`
	}{
		Channels:        restAPI.Channels,
		Context:         restAPI.Context,
		CreatedAt:       restAPI.CreatedAt,
		CreatedBy:       restAPI.CreatedBy,
		Description:     restAPI.Description,
		Id:              restAPI.Id,
		Kind:            restAPI.Kind,
		LifeCycleStatus: status,
		Name:            restAPI.Name,
		Operations:      operations,
		Policies:        restAPI.Policies,
		ProjectId:       restAPI.ProjectId,
		Transport:       restAPI.Transport,
		UpdatedAt:       restAPI.UpdatedAt,
		Upstream:        restAPI.Upstream,
		Version:         restAPI.Version,
	}, nil
}

func toDTOAPIFromImportRequest(req *api.ImportAPIProjectRequest) (*dto.API, error) {
	projectID := utils.OpenAPIUUIDToString(req.Api.ProjectId)
	apiDTO := &dto.API{
		ID:              stringValue(req.Api.Id),
		Name:            req.Api.Name,
		Kind:            stringValue(req.Api.Kind),
		Description:     stringValue(req.Api.Description),
		Context:         req.Api.Context,
		Version:         req.Api.Version,
		CreatedBy:       stringValue(req.Api.CreatedBy),
		ProjectID:       projectID,
		LifeCycleStatus: stringEnumValue(req.Api.LifeCycleStatus),
		Transport:       stringSliceValue(req.Api.Transport),
		Policies:        toDTOPolicies(req.Api.Policies),
		Operations:      toDTOOperations(req.Api.Operations),
		Channels:        toDTOChannels(req.Api.Channels),
		Upstream:        toDTOUpstream(&req.Api.Upstream),
	}

	return apiDTO, nil
}

func toDTOAPIFromRESTAPI(apiDetails *api.RESTAPI) (*dto.API, error) {
	if apiDetails == nil {
		return nil, nil
	}

	projectID := utils.OpenAPIUUIDToString(apiDetails.ProjectId)
	apiDTO := &dto.API{
		ID:              stringValue(apiDetails.Id),
		Name:            apiDetails.Name,
		Kind:            stringValue(apiDetails.Kind),
		Description:     stringValue(apiDetails.Description),
		Context:         apiDetails.Context,
		Version:         apiDetails.Version,
		CreatedBy:       stringValue(apiDetails.CreatedBy),
		ProjectID:       projectID,
		LifeCycleStatus: stringEnumValue(apiDetails.LifeCycleStatus),
		Transport:       stringSliceValue(apiDetails.Transport),
		Policies:        toDTOPolicies(apiDetails.Policies),
		Operations:      toDTOOperations(apiDetails.Operations),
		Channels:        toDTOChannels(apiDetails.Channels),
		Upstream:        toDTOUpstream(&apiDetails.Upstream),
	}

	return apiDTO, nil
}

func toAPIUpstream(upstream *dto.UpstreamConfig) api.Upstream {
	var main api.UpstreamDefinition
	if upstream != nil && upstream.Main != nil {
		main = toAPIUpstreamDefinition(upstream.Main)
	}
	var sandbox *api.UpstreamDefinition
	if upstream != nil && upstream.Sandbox != nil {
		def := toAPIUpstreamDefinition(upstream.Sandbox)
		sandbox = &def
	}

	return api.Upstream{
		Main:    main,
		Sandbox: sandbox,
	}
}

func toAPIUpstreamDefinition(endpoint *dto.UpstreamEndpoint) api.UpstreamDefinition {
	if endpoint == nil {
		return api.UpstreamDefinition{}
	}

	var auth *api.UpstreamAuth
	if endpoint.Auth != nil {
		var authType *api.UpstreamAuthType
		if endpoint.Auth.Type != "" {
			value := api.UpstreamAuthType(endpoint.Auth.Type)
			authType = &value
		}
		auth = &api.UpstreamAuth{
			Type:   authType,
			Header: utils.StringPtrIfNotEmpty(endpoint.Auth.Header),
			Value:  utils.StringPtrIfNotEmpty(endpoint.Auth.Value),
		}
	}

	return api.UpstreamDefinition{
		Auth: auth,
		Ref:  utils.StringPtrIfNotEmpty(endpoint.Ref),
		Url:  utils.StringPtrIfNotEmpty(endpoint.URL),
	}
}

func toDTOUpstream(upstream *api.Upstream) *dto.UpstreamConfig {
	if upstream == nil {
		return nil
	}

	mainEmpty := isEmptyUpstreamDefinition(upstream.Main)
	sandboxEmpty := upstream.Sandbox == nil || isEmptyUpstreamDefinition(*upstream.Sandbox)
	if mainEmpty && sandboxEmpty {
		return nil
	}

	var main *dto.UpstreamEndpoint
	if !mainEmpty {
		main = toDTOUpstreamEndpoint(upstream.Main)
	}

	var sandbox *dto.UpstreamEndpoint
	if upstream.Sandbox != nil && !sandboxEmpty {
		sandbox = toDTOUpstreamEndpoint(*upstream.Sandbox)
	}

	return &dto.UpstreamConfig{
		Main:    main,
		Sandbox: sandbox,
	}
}

func toDTOUpstreamEndpoint(definition api.UpstreamDefinition) *dto.UpstreamEndpoint {
	if isEmptyUpstreamDefinition(definition) {
		return nil
	}

	return &dto.UpstreamEndpoint{
		URL:  stringValue(definition.Url),
		Ref:  stringValue(definition.Ref),
		Auth: toDTOUpstreamAuth(definition.Auth),
	}
}

func toDTOUpstreamAuth(auth *api.UpstreamAuth) *dto.UpstreamAuth {
	if auth == nil {
		return nil
	}

	var authType string
	if auth.Type != nil {
		authType = string(*auth.Type)
	}

	if authType == "" && stringValue(auth.Header) == "" && stringValue(auth.Value) == "" {
		return nil
	}

	return &dto.UpstreamAuth{
		Type:   authType,
		Header: stringValue(auth.Header),
		Value:  stringValue(auth.Value),
	}
}

func isEmptyUpstreamDefinition(definition api.UpstreamDefinition) bool {
	if definition.Url != nil && *definition.Url != "" {
		return false
	}
	if definition.Ref != nil && *definition.Ref != "" {
		return false
	}
	if definition.Auth == nil {
		return true
	}

	if definition.Auth.Type != nil && *definition.Auth.Type != "" {
		return false
	}
	if definition.Auth.Header != nil && *definition.Auth.Header != "" {
		return false
	}
	if definition.Auth.Value != nil && *definition.Auth.Value != "" {
		return false
	}

	return true
}

func toDTOOperations(operations *[]api.Operation) []dto.Operation {
	if operations == nil {
		return nil
	}
	result := make([]dto.Operation, len(*operations))
	for i, op := range *operations {
		result[i] = dto.Operation{
			Name:        stringValue(op.Name),
			Description: stringValue(op.Description),
			Request:     toDTOOperationRequest(&op.Request),
		}
	}
	return result
}

func toDTOOperationsPtr(operations *[]api.Operation) *[]dto.Operation {
	if operations == nil {
		return nil
	}
	result := toDTOOperations(operations)
	return &result
}

func toDTOOperationRequest(req *api.OperationRequest) *dto.OperationRequest {
	if req == nil {
		return nil
	}
	return &dto.OperationRequest{
		Method:   string(req.Method),
		Path:     req.Path,
		Policies: toDTOPolicies(req.Policies),
	}
}

func toDTOChannels(channels *[]api.Channel) []dto.Channel {
	if channels == nil {
		return nil
	}
	result := make([]dto.Channel, len(*channels))
	for i, ch := range *channels {
		result[i] = dto.Channel{
			Name:        stringValue(ch.Name),
			Description: stringValue(ch.Description),
			Request:     toDTOChannelRequest(&ch.Request),
		}
	}
	return result
}

func toDTOChannelsPtr(channels *[]api.Channel) *[]dto.Channel {
	if channels == nil {
		return nil
	}
	result := toDTOChannels(channels)
	return &result
}

func toDTOChannelRequest(req *api.ChannelRequest) *dto.ChannelRequest {
	if req == nil {
		return nil
	}
	return &dto.ChannelRequest{
		Method:   string(req.Method),
		Name:     req.Name,
		Policies: toDTOPolicies(req.Policies),
	}
}

func toDTOPolicies(policies *[]api.Policy) []dto.Policy {
	if policies == nil {
		return nil
	}
	result := make([]dto.Policy, len(*policies))
	for i, policy := range *policies {
		result[i] = dto.Policy{
			ExecutionCondition: policy.ExecutionCondition,
			Name:               policy.Name,
			Params:             policy.Params,
			Version:            policy.Version,
		}
	}
	return result
}

func toDTOPoliciesPtr(policies *[]api.Policy) *[]dto.Policy {
	if policies == nil {
		return nil
	}
	result := toDTOPolicies(policies)
	return &result
}

func toAPIOperationsPtr(operations []dto.Operation) *[]api.Operation {
	if len(operations) == 0 {
		return nil
	}
	result := toAPIOperations(operations)
	return &result
}

func toAPIOperations(operations []dto.Operation) []api.Operation {
	result := make([]api.Operation, len(operations))
	for i, op := range operations {
		result[i] = api.Operation{
			Name:        utils.StringPtrIfNotEmpty(op.Name),
			Description: utils.StringPtrIfNotEmpty(op.Description),
			Request:     toAPIOperationRequest(op.Request),
		}
	}
	return result
}

func toAPIOperationRequest(req *dto.OperationRequest) api.OperationRequest {
	if req == nil {
		return api.OperationRequest{}
	}
	return api.OperationRequest{
		Method:   api.OperationRequestMethod(req.Method),
		Path:     req.Path,
		Policies: toAPIPoliciesPtr(req.Policies),
	}
}

func toAPIChannelsPtr(channels []dto.Channel) *[]api.Channel {
	if len(channels) == 0 {
		return nil
	}
	result := toAPIChannels(channels)
	return &result
}

func toAPIChannels(channels []dto.Channel) []api.Channel {
	result := make([]api.Channel, len(channels))
	for i, ch := range channels {
		result[i] = api.Channel{
			Name:        utils.StringPtrIfNotEmpty(ch.Name),
			Description: utils.StringPtrIfNotEmpty(ch.Description),
			Request:     toAPIChannelRequest(ch.Request),
		}
	}
	return result
}

func toAPIChannelRequest(req *dto.ChannelRequest) api.ChannelRequest {
	if req == nil {
		return api.ChannelRequest{}
	}
	return api.ChannelRequest{
		Method:   api.ChannelRequestMethod(req.Method),
		Name:     req.Name,
		Policies: toAPIPoliciesPtr(req.Policies),
	}
}

func toAPIPoliciesPtr(policies []dto.Policy) *[]api.Policy {
	if len(policies) == 0 {
		return nil
	}
	result := make([]api.Policy, len(policies))
	for i, policy := range policies {
		result[i] = api.Policy{
			ExecutionCondition: policy.ExecutionCondition,
			Name:               policy.Name,
			Params:             policy.Params,
			Version:            policy.Version,
		}
	}
	return &result
}

func restAPILifeCycleStatusPtr(status string) *api.RESTAPILifeCycleStatus {
	if status == "" {
		return nil
	}
	value := api.RESTAPILifeCycleStatus(status)
	return &value
}

func gatewayFunctionalityTypePtr(value string) *api.RESTAPIGatewayResponseFunctionalityType {
	if value == "" {
		return nil
	}
	converted := api.RESTAPIGatewayResponseFunctionalityType(value)
	return &converted
}

func stringSlicePtr(values []string) *[]string {
	if len(values) == 0 {
		return nil
	}
	return &values
}

func stringSliceValue(values *[]string) []string {
	if values == nil {
		return nil
	}
	return *values
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringEnumValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func stringEnumPtr[T ~string](value *T) *string {
	if value == nil {
		return nil
	}
	converted := string(*value)
	return &converted
}
