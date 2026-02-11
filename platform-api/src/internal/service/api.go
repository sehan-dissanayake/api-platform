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

package service

import (
	"errors"
	"fmt"
	"io"
	"log"
	pathpkg "path"
	"regexp"
	"strings"
	"time"

	"platform-api/src/api"
	"platform-api/src/internal/constants"
	"platform-api/src/internal/dto"
	"platform-api/src/internal/model"
	"platform-api/src/internal/repository"
	"platform-api/src/internal/utils"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"gopkg.in/yaml.v3"
)

// APIService handles business logic for API operations
type APIService struct {
	apiRepo              repository.APIRepository
	projectRepo          repository.ProjectRepository
	orgRepo              repository.OrganizationRepository
	gatewayRepo          repository.GatewayRepository
	devPortalRepo        repository.DevPortalRepository
	publicationRepo      repository.APIPublicationRepository
	gatewayEventsService *GatewayEventsService
	devPortalService     *DevPortalService
	apiUtil              *utils.APIUtil
}

// NewAPIService creates a new API service
func NewAPIService(apiRepo repository.APIRepository, projectRepo repository.ProjectRepository,
	orgRepo repository.OrganizationRepository, gatewayRepo repository.GatewayRepository,
	devPortalRepo repository.DevPortalRepository, publicationRepo repository.APIPublicationRepository,
	gatewayEventsService *GatewayEventsService, devPortalService *DevPortalService, apiUtil *utils.APIUtil) *APIService {
	return &APIService{
		apiRepo:              apiRepo,
		projectRepo:          projectRepo,
		orgRepo:              orgRepo,
		gatewayRepo:          gatewayRepo,
		devPortalRepo:        devPortalRepo,
		publicationRepo:      publicationRepo,
		gatewayEventsService: gatewayEventsService,
		devPortalService:     devPortalService,
		apiUtil:              apiUtil,
	}
}

// CreateAPI creates a new API with validation and business logic
func (s *APIService) CreateAPI(req *api.CreateRESTAPIRequest, orgUUID string) (*api.RESTAPI, error) {
	// Validate request
	if err := s.validateCreateAPIRequest(req, orgUUID); err != nil {
		return nil, err
	}

	projectID := utils.OpenAPIUUIDToString(req.ProjectId)
	// Check if project exists
	project, err := s.projectRepo.GetProjectByUUID(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, constants.ErrProjectNotFound
	}
	if project.OrganizationID != orgUUID {
		return nil, constants.ErrProjectNotFound
	}

	// Handle the API handle (user-facing identifier)
	var handle string
	if req.Id != nil && *req.Id != "" {
		handle = *req.Id
	} else {
		// Generate handle from API name with collision detection
		var err error
		handle, err = utils.GenerateHandle(req.Name, s.HandleExistsCheck(orgUUID))
		if err != nil {
			return nil, err
		}
	}

	// Set default values if not provided
	if req.CreatedBy == nil || *req.CreatedBy == "" {
		createdBy := "admin"
		req.CreatedBy = &createdBy
	}
	if req.Kind == nil || *req.Kind == "" {
		kind := constants.RestApi
		req.Kind = &kind
	}
	if req.Transport == nil || len(*req.Transport) == 0 {
		transport := []string{"http", "https"}
		req.Transport = &transport
	}
	if req.LifeCycleStatus == nil || *req.LifeCycleStatus == "" {
		status := api.CreateRESTAPIRequestLifeCycleStatus("CREATED")
		req.LifeCycleStatus = &status
	}
	if req.Operations == nil || len(*req.Operations) == 0 {
		// generate default get, post, patch and delete operations with path /*
		defaultOperations := s.generateDefaultOperations()
		req.Operations = &defaultOperations
	}

	apiDTO := s.toDTOFromCreateRequest(req, handle, orgUUID, projectID)
	apiREST, err := s.toRESTAPIFromDTO(apiDTO)
	if err != nil {
		return nil, err
	}
	apiModel := s.apiUtil.RESTAPIToModel(apiREST, orgUUID)
	// Create API in repository (UUID is generated internally by CreateAPI)
	if err := s.apiRepo.CreateAPI(apiModel); err != nil {
		return nil, fmt.Errorf("failed to create api: %w", err)
	}

	// Get the generated UUID from the model (set by CreateAPI)
	apiUUID := apiModel.ID

	// Automatically create DevPortal association for default DevPortal (use internal UUID)
	if err := s.createDefaultDevPortalAssociation(apiUUID, orgUUID); err != nil {
		// Log error but don't fail API creation if default DevPortal association fails
		log.Printf("[APIService] Failed to create default DevPortal association for API %s: %v", apiUUID, err)
	}

	return s.apiUtil.ModelToRESTAPI(apiModel)
}

// GetAPIByUUID retrieves an API by its ID
func (s *APIService) GetAPIByUUID(apiUUID, orgUUID string) (*api.RESTAPI, error) {
	if apiUUID == "" {
		return nil, errors.New("API id is required")
	}

	apiModel, err := s.apiRepo.GetAPIByUUID(apiUUID, orgUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get api: %w", err)
	}
	if apiModel == nil {
		return nil, constants.ErrAPINotFound
	}
	if apiModel.OrganizationID != orgUUID {
		return nil, constants.ErrAPINotFound
	}

	return s.apiUtil.ModelToRESTAPI(apiModel)
}

// GetAPIByHandle retrieves an API by its handle
func (s *APIService) GetAPIByHandle(handle, orgId string) (*api.RESTAPI, error) {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgId)
	if err != nil {
		return nil, err
	}
	return s.GetAPIByUUID(apiUUID, orgId)
}

// HandleExistsCheck returns a function that checks if an API handle exists in the organization.
// This is designed to be used with utils.GenerateHandle for handle generation with collision detection.
func (s *APIService) HandleExistsCheck(orgUUID string) func(string) bool {
	return func(handle string) bool {
		exists, err := s.apiRepo.CheckAPIExistsByHandleInOrganization(handle, orgUUID)
		if err != nil {
			// On error, assume it exists to be safe (will trigger retry)
			return true
		}
		return exists
	}
}

// getAPIUUIDByHandle retrieves the internal UUID for an API by its handle.
// This is a lightweight operation that only fetches minimal metadata.
func (s *APIService) getAPIUUIDByHandle(handle, orgUUID string) (string, error) {
	if handle == "" {
		return "", errors.New("API handle is required")
	}

	metadata, err := s.apiRepo.GetAPIMetadataByHandle(handle, orgUUID)
	if err != nil {
		return "", err
	}
	if metadata == nil {
		return "", constants.ErrAPINotFound
	}

	return metadata.ID, nil
}

// GetAPIsByOrganization retrieves all APIs for an organization with optional project filter
func (s *APIService) GetAPIsByOrganization(orgUUID string, projectUUID string) ([]api.RESTAPI, error) {
	// If project ID is provided, validate that it belongs to the organization
	if projectUUID != "" {
		project, err := s.projectRepo.GetProjectByUUID(projectUUID)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, constants.ErrProjectNotFound
		}
		if project.OrganizationID != orgUUID {
			return nil, constants.ErrProjectNotFound
		}
	}

	apiModels, err := s.apiRepo.GetAPIsByOrganizationUUID(orgUUID, projectUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get apis: %w", err)
	}

	apis := make([]api.RESTAPI, 0)
	for _, apiModel := range apiModels {
		apiResponse, err := s.apiUtil.ModelToRESTAPI(apiModel)
		if err != nil {
			return nil, err
		}
		if apiResponse != nil {
			apis = append(apis, *apiResponse)
		}
	}
	return apis, nil
}

// UpdateAPI updates an existing API
func (s *APIService) UpdateAPI(apiUUID string, req *api.UpdateRESTAPIRequest, orgUUID string) (*api.RESTAPI, error) {
	if apiUUID == "" {
		return nil, errors.New("API id is required")
	}

	// Get existing API
	existingAPIModel, err := s.apiRepo.GetAPIByUUID(apiUUID, orgUUID)
	if err != nil {
		return nil, err
	}
	if existingAPIModel == nil {
		return nil, constants.ErrAPINotFound
	}
	if existingAPIModel.OrganizationID != orgUUID {
		return nil, constants.ErrAPINotFound
	}

	// Apply updates using shared helper
	updatedAPI, err := s.applyAPIUpdates(existingAPIModel, req, orgUUID)
	if err != nil {
		return nil, err
	}

	// Update API in repository
	updatedAPIModel := s.apiUtil.RESTAPIToModel(updatedAPI, orgUUID)
	updatedAPIModel.ID = apiUUID // Ensure UUID remains unchanged
	if err := s.apiRepo.UpdateAPI(updatedAPIModel); err != nil {
		return nil, err
	}

	return s.apiUtil.ModelToRESTAPI(updatedAPIModel)
}

// DeleteAPI deletes an API
func (s *APIService) DeleteAPI(apiUUID, orgUUID string) error {
	if apiUUID == "" {
		return errors.New("API id is required")
	}

	// Check if API exists
	api, err := s.apiRepo.GetAPIByUUID(apiUUID, orgUUID)
	if err != nil {
		return err
	}
	if api == nil {
		return constants.ErrAPINotFound
	}
	if api.OrganizationID != orgUUID {
		return constants.ErrAPINotFound
	}

	// Get all gateway associations BEFORE deletion (associations will be cascade deleted)
	gatewayAssociations, err := s.apiRepo.GetAPIAssociations(apiUUID, constants.AssociationTypeGateway, orgUUID)
	if err != nil {
		return fmt.Errorf("failed to get gateway associations for api deletion: %w", err)
	}

	// Delete API from repository (this also deletes associations)
	if err := s.apiRepo.DeleteAPI(apiUUID, orgUUID); err != nil {
		return fmt.Errorf("failed to delete api: %w", err)
	}

	// Send deletion events to all associated gateways
	if s.gatewayEventsService != nil && gatewayAssociations != nil {
		for _, assoc := range gatewayAssociations {
			// Get gateway details to retrieve vhost
			gateway, err := s.gatewayRepo.GetByUUID(assoc.ResourceID)
			if err != nil {
				log.Printf("[WARN] Failed to get gateway for deletion event: gatewayID=%s error=%v", assoc.ResourceID, err)
				continue
			}
			if gateway == nil {
				log.Printf("[WARN] Gateway not found for deletion event: gatewayID=%s", assoc.ResourceID)
				continue
			}

			// Create and send API deletion event
			deletionEvent := &model.APIDeletionEvent{
				ApiId: apiUUID,
				Vhost: gateway.Vhost,
			}

			if err := s.gatewayEventsService.BroadcastAPIDeletionEvent(assoc.ResourceID, deletionEvent); err != nil {
				log.Printf("[WARN] Failed to broadcast API deletion event: gatewayID=%s apiUUID=%s error=%v", assoc.ResourceID, apiUUID, err)
			} else {
				log.Printf("[INFO] API deletion event sent: gatewayID=%s apiUUID=%s vhost=%s", assoc.ResourceID, apiUUID, gateway.Vhost)
			}
		}
	}

	return nil
}

// UpdateAPIByHandle updates an existing API identified by handle
func (s *APIService) UpdateAPIByHandle(handle string, req *api.UpdateRESTAPIRequest, orgId string) (*api.RESTAPI, error) {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgId)
	if err != nil {
		return nil, err
	}
	return s.UpdateAPI(apiUUID, req, orgId)
}

// DeleteAPIByHandle deletes an API identified by handle
func (s *APIService) DeleteAPIByHandle(handle, orgId string) error {
	// Get API UUID by handle
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgId)
	if err != nil {
		return err
	}

	// Delete API using existing UUID-based method
	return s.DeleteAPI(apiUUID, orgId)
}

// AddGatewaysToAPIByHandle associates multiple gateways with an API identified by handle
func (s *APIService) AddGatewaysToAPIByHandle(handle string, gatewayIds []string, orgId string) (*dto.APIGatewayListResponse, error) {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgId)
	if err != nil {
		return nil, err
	}
	return s.AddGatewaysToAPI(apiUUID, gatewayIds, orgId)
}

// GetAPIGatewaysByHandle retrieves all gateways associated with an API identified by handle
func (s *APIService) GetAPIGatewaysByHandle(handle, orgId string) (*dto.APIGatewayListResponse, error) {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgId)
	if err != nil {
		return nil, err
	}
	return s.GetAPIGateways(apiUUID, orgId)
}

// PublishAPIToDevPortalByHandle publishes an API identified by handle to a DevPortal
func (s *APIService) PublishAPIToDevPortalByHandle(handle string, req *dto.PublishToDevPortalRequest, orgID string) error {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgID)
	if err != nil {
		return err
	}
	return s.PublishAPIToDevPortal(apiUUID, req, orgID)
}

// UnpublishAPIFromDevPortalByHandle unpublishes an API identified by handle from a DevPortal
func (s *APIService) UnpublishAPIFromDevPortalByHandle(handle, devPortalUUID, orgID string) error {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgID)
	if err != nil {
		return err
	}
	return s.UnpublishAPIFromDevPortal(apiUUID, devPortalUUID, orgID)
}

// GetAPIPublicationsByHandle retrieves all DevPortals associated with an API identified by handle
func (s *APIService) GetAPIPublicationsByHandle(handle, orgID string) (*dto.APIDevPortalListResponse, error) {
	apiUUID, err := s.getAPIUUIDByHandle(handle, orgID)
	if err != nil {
		return nil, err
	}
	return s.GetAPIPublications(apiUUID, orgID)
}

// AddGatewaysToAPI associates multiple gateways with an API
func (s *APIService) AddGatewaysToAPI(apiUUID string, gatewayIds []string, orgUUID string) (*dto.APIGatewayListResponse, error) {
	// Validate that the API exists and belongs to the organization
	apiModel, err := s.apiRepo.GetAPIByUUID(apiUUID, orgUUID)
	if err != nil {
		return nil, err
	}
	if apiModel == nil {
		return nil, constants.ErrAPINotFound
	}
	if apiModel.OrganizationID != orgUUID {
		return nil, constants.ErrAPINotFound
	}

	// Validate that all gateways exist and belong to the same organization
	var validGateways []*model.Gateway
	for _, gatewayId := range gatewayIds {
		gateway, err := s.gatewayRepo.GetByUUID(gatewayId)
		if err != nil {
			return nil, err
		}
		if gateway == nil {
			return nil, constants.ErrGatewayNotFound
		}
		if gateway.OrganizationID != orgUUID {
			return nil, constants.ErrGatewayNotFound
		}
		validGateways = append(validGateways, gateway)
	}

	// Get existing associations to determine which are new vs existing
	existingAssociations, err := s.apiRepo.GetAPIAssociations(apiUUID, constants.AssociationTypeGateway, orgUUID)
	if err != nil {
		return nil, err
	}

	existingGatewayIds := make(map[string]bool)
	for _, assoc := range existingAssociations {
		existingGatewayIds[assoc.ResourceID] = true
	}

	// Process each gateway: create new associations or update existing ones
	for _, gateway := range validGateways {
		if existingGatewayIds[gateway.ID] {
			// Update existing association timestamp
			if err := s.apiRepo.UpdateAPIAssociation(apiUUID, gateway.ID, constants.AssociationTypeGateway, orgUUID); err != nil {
				return nil, err
			}
		} else {
			// Create new association
			association := &model.APIAssociation{
				ArtifactID:      apiUUID,
				OrganizationID:  orgUUID,
				ResourceID:      gateway.ID,
				AssociationType: constants.AssociationTypeGateway,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			}
			if err := s.apiRepo.CreateAPIAssociation(association); err != nil {
				return nil, err
			}
			existingGatewayIds[gateway.ID] = true
		}
	}

	// Return all gateways currently associated with the API including deployment details
	gatewayDetails, err := s.apiRepo.GetAPIGatewaysWithDetails(apiUUID, orgUUID)
	if err != nil {
		return nil, err
	}

	// Convert all associated gateways to DTOs with deployment details for response
	responses := make([]dto.APIGatewayResponse, 0, len(gatewayDetails))
	for _, gwd := range gatewayDetails {
		responses = append(responses, s.convertToAPIGatewayResponse(gwd))
	}

	// Create response with all associated gateways
	listResponse := &dto.APIGatewayListResponse{
		Count: len(responses),
		List:  responses,
		Pagination: dto.Pagination{
			Total:  len(responses),
			Offset: 0,
			Limit:  len(responses),
		},
	}

	return listResponse, nil
}

// GetAPIGateways retrieves all gateways associated with an API including deployment details
func (s *APIService) GetAPIGateways(apiUUID, orgUUID string) (*dto.APIGatewayListResponse, error) {
	// Validate that the API exists and belongs to the organization
	apiModel, err := s.apiRepo.GetAPIByUUID(apiUUID, orgUUID)
	if err != nil {
		return nil, err
	}
	if apiModel == nil {
		return nil, constants.ErrAPINotFound
	}
	if apiModel.OrganizationID != orgUUID {
		return nil, constants.ErrAPINotFound
	}

	// Get all gateways associated with this API including deployment details
	gatewayDetails, err := s.apiRepo.GetAPIGatewaysWithDetails(apiUUID, orgUUID)
	if err != nil {
		return nil, err
	}

	// Convert models to DTOs with deployment details
	responses := make([]dto.APIGatewayResponse, 0, len(gatewayDetails))
	for _, gwd := range gatewayDetails {
		responses = append(responses, s.convertToAPIGatewayResponse(gwd))
	}

	// Create paginated response
	listResponse := &dto.APIGatewayListResponse{
		Count: len(responses),
		List:  responses,
		Pagination: dto.Pagination{
			Total:  len(responses),
			Offset: 0,
			Limit:  len(responses),
		},
	}

	return listResponse, nil
}

// createDefaultDevPortalAssociation creates an association between the API and the default DevPortal
func (s *APIService) createDefaultDevPortalAssociation(apiId, orgId string) error {
	// Get default DevPortal for the organization
	defaultDevPortal, err := s.devPortalRepo.GetDefaultByOrganizationUUID(orgId)
	if err != nil {
		// If no default DevPortal exists, skip association (not an error)
		if errors.Is(err, constants.ErrDevPortalNotFound) {
			log.Printf("[APIService] No default DevPortal found for organization %s, skipping association", orgId)
			return nil
		}
		return fmt.Errorf("failed to get default DevPortal: %w", err)
	}

	// Create API-DevPortal association
	association := &model.APIAssociation{
		ArtifactID:      apiId,
		OrganizationID:  orgId,
		ResourceID:      defaultDevPortal.UUID,
		AssociationType: constants.AssociationTypeDevPortal,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.apiRepo.CreateAPIAssociation(association); err != nil {
		// Check if association already exists (shouldn't happen, but handle gracefully)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") ||
			strings.Contains(err.Error(), "duplicate key") {
			log.Printf("[APIService] API association with default DevPortal already exists for API %s", apiId)
			return nil
		}
		return fmt.Errorf("failed to create API-DevPortal association: %w", err)
	}

	log.Printf("[APIService] Successfully created association between API %s and default DevPortal %s", apiId, defaultDevPortal.UUID)
	return nil
}

// Validation methods

// validateCreateAPIRequest checks the validity of the create API request
func (s *APIService) validateCreateAPIRequest(req *api.CreateRESTAPIRequest, orgUUID string) error {
	if req.Id != nil && *req.Id != "" {
		// Validate user-provided handle
		if err := utils.ValidateHandle(*req.Id); err != nil {
			return err
		}
		// Check if handle already exists in the organization
		handleExists, err := s.apiRepo.CheckAPIExistsByHandleInOrganization(*req.Id, orgUUID)
		if err != nil {
			return err
		}
		if handleExists {
			return constants.ErrHandleExists
		}
	}
	if req.Name == "" {
		return constants.ErrInvalidAPIName
	}
	if !s.isValidContext(req.Context) {
		return constants.ErrInvalidAPIContext
	}
	if !s.isValidVersion(req.Version) {
		return constants.ErrInvalidAPIVersion
	}
	if req.ProjectId == (openapi_types.UUID{}) {
		return errors.New("project id is required")
	}

	nameVersionExists, err := s.apiRepo.CheckAPIExistsByNameAndVersionInOrganization(req.Name, req.Version, orgUUID, "")
	if err != nil {
		return err
	}
	if nameVersionExists {
		return constants.ErrAPINameVersionAlreadyExists
	}

	// Validate lifecycle status if provided
	if req.LifeCycleStatus != nil && !constants.ValidLifecycleStates[string(*req.LifeCycleStatus)] {
		return constants.ErrInvalidLifecycleState
	}

	// Validate API type if provided
	if req.Kind != nil && !strings.EqualFold(*req.Kind, constants.RestApi) {
		return constants.ErrInvalidAPIType
	}

	// Type-specific validations
	// Ensure that WebSub APIs do not have operations and HTTP APIs do not have channels
	apiKind := constants.RestApi
	if req.Kind != nil {
		apiKind = *req.Kind
	}
	switch apiKind {
	case constants.APITypeWebSub:
		// For WebSub APIs, ensure that at least one channel is defined
		if req.Operations != nil && len(*req.Operations) > 0 {
			return errors.New("WebSub APIs cannot have operations defined")
		}
	case constants.APITypeHTTP:
		// For HTTP APIs, ensure that at least one operation is defined
		if req.Channels != nil && len(*req.Channels) > 0 {
			return errors.New("HTTP APIs cannot have channels defined")
		}
	}

	// Validate transport protocols if provided
	if req.Transport != nil && len(*req.Transport) > 0 {
		for _, transport := range *req.Transport {
			if !constants.ValidTransports[strings.ToLower(transport)] {
				return constants.ErrInvalidTransport
			}
		}
	}

	return nil
}

// applyAPIUpdates applies update request fields to an existing API model and handles backend services
func (s *APIService) applyAPIUpdates(existingAPIModel *model.API, req *api.UpdateRESTAPIRequest, orgId string) (*api.RESTAPI, error) {
	// Validate update request
	if err := s.validateUpdateAPIRequest(existingAPIModel, req, orgId); err != nil {
		return nil, err
	}

	existingAPI, err := s.apiUtil.ModelToRESTAPI(existingAPIModel)
	if err != nil {
		return nil, err
	}

	// Update fields (only allow certain fields to be updated)
	if req.Name != "" {
		existingAPI.Name = req.Name
	}
	if req.Description != nil {
		existingAPI.Description = req.Description
	}
	if req.CreatedBy != nil {
		existingAPI.CreatedBy = req.CreatedBy
	}
	if req.LifeCycleStatus != nil {
		existingAPI.LifeCycleStatus = req.LifeCycleStatus
	}
	if req.Transport != nil {
		existingAPI.Transport = req.Transport
	}
	if req.Operations != nil {
		existingAPI.Operations = req.Operations
	}
	if req.Channels != nil {
		existingAPI.Channels = req.Channels
	}
	if req.Policies != nil {
		existingAPI.Policies = req.Policies
	}
	if !s.isEmptyUpstream(req.Upstream) {
		existingAPI.Upstream = req.Upstream
	}

	return existingAPI, nil
}

// validateUpdateAPIRequest checks the validity of the update API request
func (s *APIService) validateUpdateAPIRequest(existingAPIModel *model.API, req *api.UpdateRESTAPIRequest, orgUUID string) error {
	if req.Name != "" {
		nameVersionExists, err := s.apiRepo.CheckAPIExistsByNameAndVersionInOrganization(req.Name,
			existingAPIModel.Version, orgUUID, existingAPIModel.Handle)
		if err != nil {
			return err
		}
		if nameVersionExists {
			return constants.ErrAPINameVersionAlreadyExists
		}
	}

	// Validate lifecycle status if provided
	if req.LifeCycleStatus != nil && !constants.ValidLifecycleStates[string(*req.LifeCycleStatus)] {
		return constants.ErrInvalidLifecycleState
	}

	// Validate API type if provided
	if req.Kind != nil && !strings.EqualFold(*req.Kind, constants.RestApi) {
		return constants.ErrInvalidAPIType
	}

	// Validate transport protocols if provided
	if req.Transport != nil {
		for _, transport := range *req.Transport {
			if !constants.ValidTransports[strings.ToLower(transport)] {
				return constants.ErrInvalidTransport
			}
		}
	}

	return nil
}

// Helper validation methods

func (s *APIService) isValidContext(context string) bool {
	// Context can be root path (/), or follow pattern: /name, /name1/name2, /name/1.0.0, /name/v1.2.3
	pattern := `^\/(?:[a-zA-Z0-9_-]+(?:\/(?:[a-zA-Z0-9_-]+|v?\d+(?:\.\d+)?(?:\.\d+)?))*)?\/?$`
	matched, _ := regexp.MatchString(pattern, context)
	return matched && len(context) <= 232
}

func (s *APIService) isValidVersion(version string) bool {
	// Version should follow semantic versioning or simple version format
	if version == "" {
		return false
	}
	pattern := `^[^~!@#;:%^*()+={}|\\<>"'',&/$\[\]\s+\/]+$`
	matched, _ := regexp.MatchString(pattern, version)
	return matched && len(version) > 0 && len(version) <= 30
}

// isValidVHost validates vhost format
func (s *APIService) isValidVHost(vhost string) bool {
	// Basic hostname validation pattern as per RFC 1123
	pattern := `^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-ZaZ0-9\-]*[A-ZaZ0-9])$`
	matched, _ := regexp.MatchString(pattern, vhost)
	return matched
}

// generateDefaultOperations creates default CRUD operations for an API
func (s *APIService) generateDefaultOperations() []api.Operation {
	return []api.Operation{
		{
			Name:        utils.StringPtrIfNotEmpty("Get Resource"),
			Description: utils.StringPtrIfNotEmpty("Retrieve all resources"),
			Request: api.OperationRequest{
				Method:   api.OperationRequestMethodGET,
				Path:     "/*",
				Policies: &[]api.Policy{},
			},
		},
		{
			Name:        utils.StringPtrIfNotEmpty("POST Resource"),
			Description: utils.StringPtrIfNotEmpty("Create a new resource"),
			Request: api.OperationRequest{
				Method:   api.OperationRequestMethodPOST,
				Path:     "/*",
				Policies: &[]api.Policy{},
			},
		},
		{
			Name:        utils.StringPtrIfNotEmpty("Update Resource"),
			Description: utils.StringPtrIfNotEmpty("Update an existing resource"),
			Request: api.OperationRequest{
				Method:   api.OperationRequestMethodPATCH,
				Path:     "/*",
				Policies: &[]api.Policy{},
			},
		},
		{
			Name:        utils.StringPtrIfNotEmpty("Delete Resource"),
			Description: utils.StringPtrIfNotEmpty("Delete an existing resource"),
			Request: api.OperationRequest{
				Method:   api.OperationRequestMethodDELETE,
				Path:     "/*",
				Policies: &[]api.Policy{},
			},
		},
	}
}

// getDefaultChannels creates default PUB/SUB operations for an API
func (s *APIService) generateDefaultChannels(asyncAPIType *string) []api.Channel {
	if asyncAPIType != nil && *asyncAPIType == constants.APITypeWebSub {
		return []api.Channel{
			{
				Name:        utils.StringPtrIfNotEmpty("Default"),
				Description: utils.StringPtrIfNotEmpty("Default SUB Channel"),
				Request: api.ChannelRequest{
					Method:   api.SUB,
					Name:     "/_default",
					Policies: &[]api.Policy{},
				},
			},
		}
	}
	return []api.Channel{
		{
			Name:        utils.StringPtrIfNotEmpty("Default"),
			Description: utils.StringPtrIfNotEmpty("Default SUB Channel"),
			Request: api.ChannelRequest{
				Method:   api.SUB,
				Name:     "/_default",
				Policies: &[]api.Policy{},
			},
		},
		{
			Name:        utils.StringPtrIfNotEmpty("Default PUB Channel"),
			Description: utils.StringPtrIfNotEmpty("Default PUB Channel"),
			Request: api.ChannelRequest{
				Method:   api.ChannelRequestMethod("PUB"),
				Name:     "/_default",
				Policies: &[]api.Policy{},
			},
		},
	}
}

// ImportAPIProject imports an API project from a Git repository
func (s *APIService) ImportAPIProject(req *dto.ImportAPIProjectRequest, orgUUID string, gitService GitService) (*api.RESTAPI, error) {
	// 1. Validate if there is a .api-platform directory with config.yaml
	config, err := gitService.ValidateAPIProject(req.RepoURL, req.Branch, req.Path)
	if err != nil {
		if strings.Contains(err.Error(), "api project not found") {
			return nil, constants.ErrAPIProjectNotFound
		}
		if strings.Contains(err.Error(), "malformed api project") {
			return nil, constants.ErrMalformedAPIProject
		}
		if strings.Contains(err.Error(), "invalid api project") {
			return nil, constants.ErrInvalidAPIProject
		}
		return nil, err
	}

	// For now, we'll process the first API in the config (can be extended later for multiple APIs)
	if len(config.APIs) == 0 {
		return nil, constants.ErrMalformedAPIProject
	}

	apiConfig := config.APIs[0]

	// 5. Fetch the WSO2 artifact file content
	wso2ArtifactClean := pathpkg.Clean(apiConfig.WSO2Artifact)
	wso2ArtifactPath := pathpkg.Join(req.Path, wso2ArtifactClean)
	artifactData, err := gitService.FetchWSO2Artifact(req.RepoURL, req.Branch, wso2ArtifactPath)
	if err != nil {
		return nil, constants.ErrWSO2ArtifactNotFound
	}

	// 6. Create API with details from WSO2 artifact, overwritten by request details
	apiData := s.mergeAPIData(&artifactData.Spec, &req.API)

	// 7. Create API using the existing CreateAPI flow
	createReq, err := s.toCreateRESTAPIRequest(apiData)
	if err != nil {
		return nil, err
	}

	return s.CreateAPI(createReq, orgUUID)
}

// mergeAPIData merges WSO2 artifact data with user-provided API data (user data takes precedence)
func (s *APIService) mergeAPIData(artifact *dto.APIYAMLData, userAPIData *dto.API) *dto.API {
	apiDTO := s.apiUtil.APIYAMLDataToDTO(artifact)

	// Overwrite with user-provided data (if not empty)
	if userAPIData.ID != "" {
		apiDTO.ID = userAPIData.ID
	}
	if userAPIData.Name != "" {
		apiDTO.Name = userAPIData.Name
	}
	if userAPIData.Description != "" {
		apiDTO.Description = userAPIData.Description
	}
	if userAPIData.Context != "" {
		apiDTO.Context = userAPIData.Context
	}
	if userAPIData.Version != "" {
		apiDTO.Version = userAPIData.Version
	}
	if userAPIData.CreatedBy != "" {
		apiDTO.CreatedBy = userAPIData.CreatedBy
	}
	if userAPIData.ProjectID != "" {
		apiDTO.ProjectID = userAPIData.ProjectID
	}
	if userAPIData.LifeCycleStatus != "" {
		apiDTO.LifeCycleStatus = userAPIData.LifeCycleStatus
	}
	if len(userAPIData.Transport) > 0 {
		apiDTO.Transport = userAPIData.Transport
	}

	return apiDTO
}

// ValidateAndRetrieveAPIProject validates an API project from Git repository with comprehensive checks
func (s *APIService) ValidateAndRetrieveAPIProject(req *dto.ValidateAPIProjectRequest,
	gitService GitService) (*dto.APIProjectValidationResponse, error) {
	response := &dto.APIProjectValidationResponse{
		IsAPIProjectValid:    false,
		IsAPIConfigValid:     false,
		IsAPIDefinitionValid: false,
		Errors:               []string{},
	}

	// Step 1: Check if .api-platform directory exists and validate config
	config, err := gitService.ValidateAPIProject(req.RepoURL, req.Branch, req.Path)
	if err != nil {
		response.Errors = append(response.Errors, err.Error())
		return response, nil
	}

	// Process the first API entry (assuming single API per project for now)
	apiEntry := config.APIs[0]

	// Step 3: Fetch OpenAPI definition
	openAPIClean := pathpkg.Clean(apiEntry.OpenAPI)
	openAPIPath := pathpkg.Join(req.Path, openAPIClean)
	openAPIContent, err := gitService.FetchFileContent(req.RepoURL, req.Branch, openAPIPath)
	if err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("failed to fetch OpenAPI file: %s", err.Error()))
		return response, nil
	}

	// Basic OpenAPI validation (check if it's valid YAML/JSON with required fields)
	if err := s.apiUtil.ValidateOpenAPIDefinition(openAPIContent); err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("invalid OpenAPI definition: %s", err.Error()))
		return response, nil
	}

	response.IsAPIDefinitionValid = true

	// Step 4: Fetch WSO2 artifact (api.yaml)
	wso2ArtifactClean := pathpkg.Clean(apiEntry.WSO2Artifact)
	wso2ArtifactPath := pathpkg.Join(req.Path, wso2ArtifactClean)
	wso2ArtifactContent, err := gitService.FetchFileContent(req.RepoURL, req.Branch, wso2ArtifactPath)
	if err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("failed to fetch WSO2 artifact file: %s", err.Error()))
		return response, nil
	}

	var wso2Artifact dto.APIDeploymentYAML
	if err := yaml.Unmarshal(wso2ArtifactContent, &wso2Artifact); err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("invalid WSO2 artifact format: %s", err.Error()))
		return response, nil
	}

	// Step 5: Validate WSO2 artifact structure
	if err := s.apiUtil.ValidateWSO2Artifact(&wso2Artifact); err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("invalid WSO2 artifact: %s", err.Error()))
		return response, nil
	}

	response.IsAPIConfigValid = true

	// Step 6: Check if OpenAPI and WSO2 artifact match (optional validation)
	if err := s.apiUtil.ValidateAPIDefinitionConsistency(openAPIContent, &wso2Artifact); err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("API definitions mismatch: %s", err.Error()))
		response.IsAPIProjectValid = false
		return response, nil
	}

	// Step 7: If all validations pass, convert to API DTO
	api, err := s.apiUtil.ConvertAPIYAMLDataToDTO(&wso2Artifact)
	if err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("failed to convert API data: %s", err.Error()))
		return response, nil
	}

	response.API = api
	response.IsAPIProjectValid = response.IsAPIConfigValid && response.IsAPIDefinitionValid

	return response, nil
}

// PublishAPIToDevPortal publishes an API to a specific DevPortal
func (s *APIService) PublishAPIToDevPortal(apiID string, req *dto.PublishToDevPortalRequest, orgID string) error {
	// Get the API
	apiREST, err := s.GetAPIByUUID(apiID, orgID)
	if err != nil {
		return err
	}

	apiDTO, err := s.toDTOFromRESTAPI(apiREST)
	if err != nil {
		return err
	}

	// Publish API to DevPortal
	return s.devPortalService.PublishAPIToDevPortal(apiDTO, req, orgID)
}

// UnpublishAPIFromDevPortal unpublishes an API from a specific DevPortal
func (s *APIService) UnpublishAPIFromDevPortal(apiID, devPortalUUID, orgID string) error {
	// Unpublish API from DevPortal
	return s.devPortalService.UnpublishAPIFromDevPortal(devPortalUUID, orgID, apiID)
}

// GetAPIPublications retrieves all DevPortals associated with an API including publication details
// This mirrors the GetAPIGateways implementation for consistency
func (s *APIService) GetAPIPublications(apiUUID, orgUUID string) (*dto.APIDevPortalListResponse, error) {
	// Validate that the API exists and belongs to the organization
	apiModel, err := s.apiRepo.GetAPIByUUID(apiUUID, orgUUID)
	if err != nil {
		return nil, err
	}
	if apiModel == nil {
		return nil, constants.ErrAPINotFound
	}
	if apiModel.OrganizationID != orgUUID {
		return nil, constants.ErrAPINotFound
	}

	// Get all DevPortals associated with this API including publication details
	devPortalDetails, err := s.publicationRepo.GetAPIDevPortalsWithDetails(apiUUID, orgUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get API-DevPortal associations: %w", err)
	}

	// Convert models to DTOs with publication details
	responses := make([]dto.APIDevPortalResponse, 0, len(devPortalDetails))
	for _, dpd := range devPortalDetails {
		responses = append(responses, s.convertToAPIDevPortalResponse(dpd))
	}

	// Create paginated response
	listResponse := &dto.APIDevPortalListResponse{
		Count: len(responses),
		List:  responses,
		Pagination: dto.Pagination{
			Total:  len(responses),
			Offset: 0,
			Limit:  len(responses),
		},
	}

	return listResponse, nil
}

// convertToAPIGatewayResponse converts APIGatewayWithDetails to APIGatewayResponse
func (s *APIService) convertToAPIGatewayResponse(gwd *model.APIGatewayWithDetails) dto.APIGatewayResponse {
	// Create the base gateway response
	gatewayResponse := dto.GatewayResponse{
		ID:                gwd.ID,
		OrganizationID:    gwd.OrganizationID,
		Name:              gwd.Name,
		DisplayName:       gwd.DisplayName,
		Description:       gwd.Description,
		Vhost:             gwd.Vhost,
		IsCritical:        gwd.IsCritical,
		FunctionalityType: gwd.FunctionalityType,
		IsActive:          gwd.IsActive,
		CreatedAt:         gwd.CreatedAt,
		UpdatedAt:         gwd.UpdatedAt,
	}

	// Create API gateway response with embedded gateway response
	apiGatewayResponse := dto.APIGatewayResponse{
		GatewayResponse: gatewayResponse,
		AssociatedAt:    gwd.AssociatedAt,
		IsDeployed:      gwd.IsDeployed,
	}

	// Add deployment details if deployed
	if gwd.IsDeployed && gwd.DeploymentID != nil && gwd.DeployedAt != nil {
		apiGatewayResponse.Deployment = &dto.DeploymentDetails{
			DeploymentID: *gwd.DeploymentID,
			DeployedAt:   *gwd.DeployedAt,
		}
	}

	return apiGatewayResponse
}

// convertToAPIDevPortalResponse converts APIDevPortalWithDetails to APIDevPortalResponse
func (s *APIService) convertToAPIDevPortalResponse(dpd *model.APIDevPortalWithDetails) dto.APIDevPortalResponse {
	// Create the base DevPortal response
	devPortalResponse := dto.DevPortalResponse{
		UUID:             dpd.UUID,
		OrganizationUUID: dpd.OrganizationUUID,
		Name:             dpd.Name,
		Identifier:       dpd.Identifier,
		UIUrl:            fmt.Sprintf("%s/%s/views/default/apis", dpd.APIUrl, dpd.Identifier), // Computed field
		APIUrl:           dpd.APIUrl,
		Hostname:         dpd.Hostname,
		IsActive:         dpd.IsActive,
		IsEnabled:        dpd.IsEnabled,
		HeaderKeyName:    "", // Not included in response for security
		IsDefault:        dpd.IsDefault,
		Visibility:       dpd.Visibility,
		Description:      dpd.Description,
		CreatedAt:        dpd.CreatedAt,
		UpdatedAt:        dpd.UpdatedAt,
	}

	// Create API DevPortal response with embedded DevPortal response
	apiDevPortalResponse := dto.APIDevPortalResponse{
		DevPortalResponse: devPortalResponse,
		AssociatedAt:      dpd.AssociatedAt,
		IsPublished:       dpd.IsPublished,
	}

	// Add publication details if published
	if dpd.IsPublished && dpd.PublishedAt != nil {
		status := ""
		if dpd.PublicationStatus != nil {
			status = *dpd.PublicationStatus
		}
		apiVersion := ""
		if dpd.APIVersion != nil {
			apiVersion = *dpd.APIVersion
		}
		devPortalRefID := ""
		if dpd.DevPortalRefID != nil {
			devPortalRefID = *dpd.DevPortalRefID
		}
		sandboxEndpoint := ""
		if dpd.SandboxEndpointURL != nil {
			sandboxEndpoint = *dpd.SandboxEndpointURL
		}
		productionEndpoint := ""
		if dpd.ProductionEndpointURL != nil {
			productionEndpoint = *dpd.ProductionEndpointURL
		}
		updatedAt := time.Now()
		if dpd.PublicationUpdatedAt != nil {
			updatedAt = *dpd.PublicationUpdatedAt
		}

		apiDevPortalResponse.Publication = &dto.APIPublicationDetails{
			Status:             status,
			APIVersion:         apiVersion,
			DevPortalRefID:     devPortalRefID,
			SandboxEndpoint:    sandboxEndpoint,
			ProductionEndpoint: productionEndpoint,
			PublishedAt:        *dpd.PublishedAt,
			UpdatedAt:          updatedAt,
		}
	}

	return apiDevPortalResponse
}

// ValidateOpenAPIDefinition validates an OpenAPI definition from multipart form data
func (s *APIService) ValidateOpenAPIDefinition(req *dto.ValidateOpenAPIRequest) (*dto.OpenAPIValidationResponse, error) {
	response := &dto.OpenAPIValidationResponse{
		IsAPIDefinitionValid: false,
		Errors:               []string{},
	}

	var content []byte
	var err error

	// If URL is provided, fetch content from URL
	if req.URL != "" {
		content, err = s.apiUtil.FetchOpenAPIFromURL(req.URL)
		if err != nil {
			content = make([]byte, 0)
			response.Errors = append(response.Errors, fmt.Sprintf("failed to fetch OpenAPI from URL: %s", err.Error()))
		}
	}

	// If definition file is provided, read from file
	if req.Definition != nil {
		file, err := req.Definition.Open()
		if err != nil {
			response.Errors = append(response.Errors, fmt.Sprintf("failed to open definition file: %s", err.Error()))
			return response, nil
		}
		defer file.Close()

		content, err = io.ReadAll(file)
		if err != nil {
			response.Errors = append(response.Errors, fmt.Sprintf("failed to read definition file: %s", err.Error()))
			return response, nil
		}
	}

	// If neither URL nor file is provided
	if len(content) == 0 {
		response.Errors = append(response.Errors, "either URL or definition file must be provided")
		return response, nil
	}

	// Validate the OpenAPI definition
	if err := s.apiUtil.ValidateOpenAPIDefinition(content); err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("invalid OpenAPI definition: %s", err.Error()))
		return response, nil
	}

	// Parse API specification to extract metadata directly into API DTO using libopenapi
	api, err := s.apiUtil.ParseAPIDefinition(content)
	if err != nil {
		response.Errors = append(response.Errors, fmt.Sprintf("failed to parse API specification: %s", err.Error()))
		return response, nil
	}

	// Set the parsed API for response
	response.IsAPIDefinitionValid = true
	response.API = api

	return response, nil
}

// ImportFromOpenAPI imports an API from an OpenAPI definition
func (s *APIService) ImportFromOpenAPI(req *dto.ImportOpenAPIRequest, orgId string) (*api.RESTAPI, error) {
	var content []byte
	var err error
	var errorList []string

	// If URL is provided, fetch content from URL
	if req.URL != "" {
		content, err = s.apiUtil.FetchOpenAPIFromURL(req.URL)
		if err != nil {
			content = make([]byte, 0)
			errorList = append(errorList, fmt.Sprintf("failed to fetch OpenAPI from URL: %s", err.Error()))
		}
	}

	// If definition file is provided, read from file
	if req.Definition != nil {
		file, err := req.Definition.Open()
		if err != nil {
			errorList = append(errorList, fmt.Sprintf("failed to open OpenAPI definition file: %s", err.Error()))
			return nil, errors.New(strings.Join(errorList, "; "))
		}
		defer file.Close()

		content, err = io.ReadAll(file)
		if err != nil {
			errorList = append(errorList, fmt.Sprintf("failed to read OpenAPI definition file: %s", err.Error()))
			return nil, errors.New(strings.Join(errorList, "; "))
		}
	}

	// If neither URL nor file is provided
	if len(content) == 0 {
		errorList = append(errorList, "either URL or definition file must be provided")
		return nil, errors.New(strings.Join(errorList, "; "))
	}

	// Validate and parse the OpenAPI definition
	apiDetails, err := s.apiUtil.ValidateAndParseOpenAPI(content)
	if err != nil {
		return nil, fmt.Errorf("failed to validate and parse OpenAPI definition: %w", err)
	}

	// Merge provided API details with extracted details from OpenAPI
	mergedAPI := s.apiUtil.MergeAPIDetails(&req.API, apiDetails)
	if mergedAPI == nil {
		return nil, errors.New("failed to merge API details")
	}

	// Create API using existing CreateAPI logic
	createReq, err := s.toCreateRESTAPIRequest(mergedAPI)
	if err != nil {
		return nil, err
	}

	err = s.validateCreateAPIRequest(createReq, orgId)
	if err != nil {
		return nil, fmt.Errorf("validation failed for merged API details: %w", err)
	}

	// Create the API
	return s.CreateAPI(createReq, orgId)
}

// ValidateAPI validates if an API with the given identifier or name+version combination exists within an organization
func (s *APIService) ValidateAPI(req *dto.APIValidationRequest, orgUUID string) (*dto.APIValidationResponse, error) {
	// Validate request - either identifier OR both name and version must be provided
	if req.Identifier == "" && (req.Name == "" || req.Version == "") {
		return nil, errors.New("either 'identifier' or both 'name' and 'version' parameters are required")
	}

	// Check if organization exists
	organization, err := s.orgRepo.GetOrganizationByUUID(orgUUID)
	if err != nil {
		return nil, err
	}
	if organization == nil {
		return nil, constants.ErrOrganizationNotFound
	}

	var exists bool
	var validationError *dto.APIValidationError

	// Check existence based on the provided parameters
	if req.Identifier != "" {
		// Validate by identifier
		exists, err = s.apiRepo.CheckAPIExistsByHandleInOrganization(req.Identifier, orgUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to check API existence by identifier: %w", err)
		}
		if exists {
			validationError = &dto.APIValidationError{
				Code:    "api-identifier-already-exists",
				Message: fmt.Sprintf("An API with identifier '%s' already exists in the organization.", req.Identifier),
			}
		}
	} else {
		// Validate by name and version
		exists, err = s.apiRepo.CheckAPIExistsByNameAndVersionInOrganization(req.Name, req.Version, orgUUID, "")
		if err != nil {
			return nil, fmt.Errorf("failed to check API existence by name and version: %w", err)
		}
		if exists {
			validationError = &dto.APIValidationError{
				Code: "api-name-version-already-exists",
				Message: fmt.Sprintf("The API name '%s' with version '%s' already exists in the organization.",
					req.Name, req.Version),
			}
		}
	}

	// Create response
	response := &dto.APIValidationResponse{
		Valid: !exists, // valid means the API doesn't exist (available for use)
		Error: validationError,
	}

	return response, nil
}

func (s *APIService) toDTOFromCreateRequest(req *api.CreateRESTAPIRequest, handle, orgUUID, projectID string) *dto.API {
	return &dto.API{
		ID:              handle,
		Name:            req.Name,
		Kind:            stringValue(req.Kind),
		Description:     stringValue(req.Description),
		Context:         req.Context,
		Version:         req.Version,
		CreatedBy:       stringValue(req.CreatedBy),
		ProjectID:       projectID,
		OrganizationID:  orgUUID,
		LifeCycleStatus: stringEnumValue(req.LifeCycleStatus),
		Transport:       stringSliceValue(req.Transport),
		Operations:      s.toDTOOperationsFromAPI(req.Operations),
		Channels:        s.toDTOChannelsFromAPI(req.Channels),
		Policies:        s.toDTOPoliciesFromAPI(req.Policies),
		Upstream:        s.toDTOUpstreamFromAPI(req.Upstream),
	}
}

func (s *APIService) toCreateRESTAPIRequest(apiDTO *dto.API) (*api.CreateRESTAPIRequest, error) {
	if apiDTO == nil {
		return nil, nil
	}

	projectID, err := utils.ParseOpenAPIUUID(apiDTO.ProjectID)
	if err != nil {
		return nil, err
	}

	request := &api.CreateRESTAPIRequest{
		Name:      apiDTO.Name,
		Context:   apiDTO.Context,
		Version:   apiDTO.Version,
		ProjectId: *projectID,
		Upstream:  s.toAPIUpstreamFromDTO(apiDTO.Upstream),
	}

	if apiDTO.ID != "" {
		request.Id = &apiDTO.ID
	}
	if apiDTO.Description != "" {
		request.Description = &apiDTO.Description
	}
	if apiDTO.CreatedBy != "" {
		request.CreatedBy = &apiDTO.CreatedBy
	}
	if apiDTO.Kind != "" {
		request.Kind = &apiDTO.Kind
	}
	if apiDTO.LifeCycleStatus != "" {
		status := api.CreateRESTAPIRequestLifeCycleStatus(apiDTO.LifeCycleStatus)
		request.LifeCycleStatus = &status
	}
	if len(apiDTO.Transport) > 0 {
		request.Transport = &apiDTO.Transport
	}
	if ops := s.toAPIOperationsFromDTO(apiDTO.Operations); ops != nil {
		request.Operations = ops
	}
	if channels := s.toAPIChannelsFromDTO(apiDTO.Channels); channels != nil {
		request.Channels = channels
	}
	if policies := s.toAPIPoliciesFromDTO(apiDTO.Policies); policies != nil {
		request.Policies = policies
	}

	return request, nil
}

func (s *APIService) toDTOFromRESTAPI(apiREST *api.RESTAPI) (*dto.API, error) {
	if apiREST == nil {
		return nil, nil
	}

	projectID := utils.OpenAPIUUIDToString(apiREST.ProjectId)

	return &dto.API{
		ID:              stringValue(apiREST.Id),
		Name:            apiREST.Name,
		Kind:            stringValue(apiREST.Kind),
		Description:     stringValue(apiREST.Description),
		Context:         apiREST.Context,
		Version:         apiREST.Version,
		CreatedBy:       stringValue(apiREST.CreatedBy),
		ProjectID:       projectID,
		LifeCycleStatus: stringEnumValue(apiREST.LifeCycleStatus),
		Transport:       stringSliceValue(apiREST.Transport),
		Operations:      s.toDTOOperationsFromAPI(apiREST.Operations),
		Channels:        s.toDTOChannelsFromAPI(apiREST.Channels),
		Policies:        s.toDTOPoliciesFromAPI(apiREST.Policies),
		Upstream:        s.toDTOUpstreamFromAPI(apiREST.Upstream),
	}, nil
}

func (s *APIService) toRESTAPIFromDTO(apiDTO *dto.API) (*api.RESTAPI, error) {
	if apiDTO == nil {
		return nil, nil
	}

	projectID, err := utils.ParseOpenAPIUUID(apiDTO.ProjectID)
	if err != nil {
		return nil, err
	}

	response := &api.RESTAPI{
		Channels:        s.toAPIChannelsFromDTO(apiDTO.Channels),
		Context:         apiDTO.Context,
		CreatedAt:       utils.TimePtrIfNotZero(apiDTO.CreatedAt),
		CreatedBy:       utils.StringPtrIfNotEmpty(apiDTO.CreatedBy),
		Description:     utils.StringPtrIfNotEmpty(apiDTO.Description),
		Id:              utils.StringPtrIfNotEmpty(apiDTO.ID),
		Kind:            utils.StringPtrIfNotEmpty(apiDTO.Kind),
		LifeCycleStatus: restAPILifeCycleStatusPtr(apiDTO.LifeCycleStatus),
		Name:            apiDTO.Name,
		Operations:      s.toAPIOperationsFromDTO(apiDTO.Operations),
		Policies:        s.toAPIPoliciesFromDTO(apiDTO.Policies),
		ProjectId:       *projectID,
		Transport:       stringSlicePtr(apiDTO.Transport),
		UpdatedAt:       utils.TimePtrIfNotZero(apiDTO.UpdatedAt),
		Upstream:        s.toAPIUpstreamFromDTO(apiDTO.Upstream),
		Version:         apiDTO.Version,
	}

	return response, nil
}

func (s *APIService) toDTOOperationsFromAPI(operations *[]api.Operation) []dto.Operation {
	if operations == nil {
		return nil
	}
	result := make([]dto.Operation, len(*operations))
	for i, op := range *operations {
		result[i] = dto.Operation{
			Name:        stringValue(op.Name),
			Description: stringValue(op.Description),
			Request:     s.toDTOOperationRequestFromAPI(&op.Request),
		}
	}
	return result
}

func (s *APIService) toDTOOperationRequestFromAPI(req *api.OperationRequest) *dto.OperationRequest {
	if req == nil {
		return nil
	}
	return &dto.OperationRequest{
		Method:   string(req.Method),
		Path:     req.Path,
		Policies: s.toDTOPoliciesFromAPI(req.Policies),
	}
}

func (s *APIService) toDTOChannelsFromAPI(channels *[]api.Channel) []dto.Channel {
	if channels == nil {
		return nil
	}
	result := make([]dto.Channel, len(*channels))
	for i, ch := range *channels {
		result[i] = dto.Channel{
			Name:        stringValue(ch.Name),
			Description: stringValue(ch.Description),
			Request:     s.toDTOChannelRequestFromAPI(&ch.Request),
		}
	}
	return result
}

func (s *APIService) toDTOChannelRequestFromAPI(req *api.ChannelRequest) *dto.ChannelRequest {
	if req == nil {
		return nil
	}
	return &dto.ChannelRequest{
		Method:   string(req.Method),
		Name:     req.Name,
		Policies: s.toDTOPoliciesFromAPI(req.Policies),
	}
}

func (s *APIService) toDTOPoliciesFromAPI(policies *[]api.Policy) []dto.Policy {
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

func (s *APIService) toDTOUpstreamFromAPI(upstream api.Upstream) *dto.UpstreamConfig {
	if s.isEmptyUpstream(upstream) {
		return nil
	}

	var main *dto.UpstreamEndpoint
	if !s.isEmptyUpstreamDefinition(upstream.Main) {
		main = s.toDTOUpstreamEndpointFromAPI(upstream.Main)
	}

	var sandbox *dto.UpstreamEndpoint
	if upstream.Sandbox != nil && !s.isEmptyUpstreamDefinition(*upstream.Sandbox) {
		sandbox = s.toDTOUpstreamEndpointFromAPI(*upstream.Sandbox)
	}

	return &dto.UpstreamConfig{
		Main:    main,
		Sandbox: sandbox,
	}
}

func (s *APIService) toDTOUpstreamEndpointFromAPI(definition api.UpstreamDefinition) *dto.UpstreamEndpoint {
	if s.isEmptyUpstreamDefinition(definition) {
		return nil
	}

	return &dto.UpstreamEndpoint{
		URL:  stringValue(definition.Url),
		Ref:  stringValue(definition.Ref),
		Auth: s.toDTOUpstreamAuthFromAPI(definition.Auth),
	}
}

func (s *APIService) toDTOUpstreamAuthFromAPI(auth *api.UpstreamAuth) *dto.UpstreamAuth {
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

func (s *APIService) isEmptyUpstream(upstream api.Upstream) bool {
	if !s.isEmptyUpstreamDefinition(upstream.Main) {
		return false
	}
	if upstream.Sandbox != nil && !s.isEmptyUpstreamDefinition(*upstream.Sandbox) {
		return false
	}
	return true
}

func (s *APIService) isEmptyUpstreamDefinition(definition api.UpstreamDefinition) bool {
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

func (s *APIService) toAPIOperationsFromDTO(operations []dto.Operation) *[]api.Operation {
	if len(operations) == 0 {
		return nil
	}
	result := make([]api.Operation, len(operations))
	for i, op := range operations {
		if op.Request == nil {
			result[i] = api.Operation{
				Name:        utils.StringPtrIfNotEmpty(op.Name),
				Description: utils.StringPtrIfNotEmpty(op.Description),
				Request:     api.OperationRequest{},
			}
			continue
		}
		result[i] = api.Operation{
			Name:        utils.StringPtrIfNotEmpty(op.Name),
			Description: utils.StringPtrIfNotEmpty(op.Description),
			Request: api.OperationRequest{
				Method:   api.OperationRequestMethod(op.Request.Method),
				Path:     op.Request.Path,
				Policies: s.toAPIPoliciesFromDTO(op.Request.Policies),
			},
		}
	}
	return &result
}

func (s *APIService) toAPIChannelsFromDTO(channels []dto.Channel) *[]api.Channel {
	if len(channels) == 0 {
		return nil
	}
	result := make([]api.Channel, len(channels))
	for i, ch := range channels {
		if ch.Request == nil {
			result[i] = api.Channel{
				Name:        utils.StringPtrIfNotEmpty(ch.Name),
				Description: utils.StringPtrIfNotEmpty(ch.Description),
				Request:     api.ChannelRequest{},
			}
			continue
		}
		result[i] = api.Channel{
			Name:        utils.StringPtrIfNotEmpty(ch.Name),
			Description: utils.StringPtrIfNotEmpty(ch.Description),
			Request: api.ChannelRequest{
				Method:   api.ChannelRequestMethod(ch.Request.Method),
				Name:     ch.Request.Name,
				Policies: s.toAPIPoliciesFromDTO(ch.Request.Policies),
			},
		}
	}
	return &result
}

func (s *APIService) toAPIPoliciesFromDTO(policies []dto.Policy) *[]api.Policy {
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

func (s *APIService) toAPIUpstreamFromDTO(upstream *dto.UpstreamConfig) api.Upstream {
	var main api.UpstreamDefinition
	if upstream != nil && upstream.Main != nil {
		main = api.UpstreamDefinition{
			Auth: s.toAPIUpstreamAuthFromDTO(upstream.Main.Auth),
			Ref:  utils.StringPtrIfNotEmpty(upstream.Main.Ref),
			Url:  utils.StringPtrIfNotEmpty(upstream.Main.URL),
		}
	}

	var sandbox *api.UpstreamDefinition
	if upstream != nil && upstream.Sandbox != nil {
		def := api.UpstreamDefinition{
			Auth: s.toAPIUpstreamAuthFromDTO(upstream.Sandbox.Auth),
			Ref:  utils.StringPtrIfNotEmpty(upstream.Sandbox.Ref),
			Url:  utils.StringPtrIfNotEmpty(upstream.Sandbox.URL),
		}
		sandbox = &def
	}

	return api.Upstream{
		Main:    main,
		Sandbox: sandbox,
	}
}

func (s *APIService) toAPIUpstreamAuthFromDTO(auth *dto.UpstreamAuth) *api.UpstreamAuth {
	if auth == nil {
		return nil
	}
	var authType *api.UpstreamAuthType
	if auth.Type != "" {
		value := api.UpstreamAuthType(auth.Type)
		authType = &value
	}
	return &api.UpstreamAuth{
		Type:   authType,
		Header: utils.StringPtrIfNotEmpty(auth.Header),
		Value:  utils.StringPtrIfNotEmpty(auth.Value),
	}
}

func restAPILifeCycleStatusPtr(status string) *api.RESTAPILifeCycleStatus {
	if status == "" {
		return nil
	}
	value := api.RESTAPILifeCycleStatus(status)
	return &value
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

func stringSliceValue(values *[]string) []string {
	if values == nil {
		return nil
	}
	return *values
}

func stringSlicePtr(values []string) *[]string {
	if len(values) == 0 {
		return nil
	}
	return &values
}
