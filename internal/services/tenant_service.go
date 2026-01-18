package services

import (
	"context"
	"errors"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/repository"
)

// TenantService handles tenant logic.
type TenantService struct {
	repo repository.TenantRepository
}

// NewTenantService constructs a TenantService.
func NewTenantService(repo repository.TenantRepository) *TenantService {
	return &TenantService{repo: repo}
}

// creates a new tenant.
// Handles timestamps, validation, and domain uniqueness.
func (s *TenantService) Create(ctx context.Context, name, domain string) error {
	if name == "" || domain == "" {
		return errors.New("name and domain are required")
	}

	tenant := &models.Tenant{
		Name:   name,
		Domain: domain,
		Status: models.TenantStatusActive,
	}

	return s.repo.Create(ctx, tenant)
}

// UpdateTenant updates name/domain/status of a tenant.
// All timestamps are handled here.
func (s *TenantService) Update(ctx context.Context, tenant *models.Tenant) error {
	if tenant.ID == "" {
		return repository.ErrTenantNotFound
	}
	return s.repo.Update(ctx, tenant)
}

// InactiveTenant Inactives a tenant (sets status = Inactive)
func (s *TenantService) Inactive(ctx context.Context, tenantID string) error {

	tenant, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		return err
	}

	tenant.Status = models.TenantStatusInactive
	return s.repo.Update(ctx, tenant)
}

// ActivateTenant Activates a tenant (sets status = Active)
func (s *TenantService) Activate(ctx context.Context, tenantID string) error {

	tenant, err := s.repo.GetByID(ctx, tenantID)
	if err != nil {
		return err
	}

	tenant.Status = models.TenantStatusActive
	return s.repo.Update(ctx, tenant)
}

// GetByDomain retrieves tenant and ensures single tenant
func (s *TenantService) GetByDomain(ctx context.Context, domain string) (*models.Tenant, error) {
	return s.repo.GetByDomain(ctx, domain)
}
