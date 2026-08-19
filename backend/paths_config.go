package backend

import (
	"context"

	"github.com/openbao/openbao/sdk/v2/framework"
	"github.com/openbao/openbao/sdk/v2/logical"
)

// pathConfig defines `vs/admin/config`. It is a
// single write-only-as-a-whole resource (no Create/Update
// distinction, no ExistenceCheck): every write is a full replacement,
// which matches role_id/secret_id/api_addr being required on every
// write.
func pathConfig(b *Backend) *framework.Path {
	return &framework.Path{
		Pattern: "admin/config",

		Fields: map[string]*framework.FieldSchema{
			"approle_mount": {
				Type:        framework.TypeString,
				Default:     defaultApproleMount,
				Description: "Auth mount for the loopback AppRole.",
			},
			"role_id": {
				Type:        framework.TypeString,
				Required:    true,
				Description: "Loopback AppRole role_id.",
			},
			"secret_id": {
				Type:        framework.TypeString,
				Required:    true,
				Description: "Loopback AppRole secret_id. Write-only; never returned on read.",
			},
			"api_addr": {
				Type:        framework.TypeString,
				Required:    true,
				Description: "Loopback address the engine dereferences targets against.",
			},
			"expose_targets": {
				Type:        framework.TypeBool,
				Default:     false,
				Description: "Include the target mount in synthetic metadata/error responses.",
			},
			"target_mount_allowlist": {
				Type:        framework.TypeCommaStringSlice,
				Description: "Exact mount names mapping targets are restricted to. Absent means every mount except the fixed reject list is permitted.",
			},
		},

		Operations: map[logical.Operation]framework.OperationHandler{
			logical.ReadOperation: &framework.PathOperation{
				Callback: b.pathConfigRead,
				Summary:  "Read the voidstar engine configuration (secret_id concealed).",
			},
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.pathConfigWrite,
				Summary:  "Write the voidstar engine configuration.",
			},
		},

		HelpSynopsis:    "Configure the voidstar secrets engine.",
		HelpDescription: "Configure the loopback AppRole, api_addr, and target mount allowlist for the voidstar secrets engine.",
	}
}

func (b *Backend) pathConfigRead(ctx context.Context, req *logical.Request, _ *framework.FieldData) (*logical.Response, error) {
	cfg, err := getConfigFromStorage(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return &logical.Response{Data: configResponseData(cfg)}, nil
}

func (b *Backend) pathConfigWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	cfg := &Config{
		ApproleMount:         data.Get("approle_mount").(string),
		RoleID:               data.Get("role_id").(string),
		SecretID:             data.Get("secret_id").(string),
		APIAddr:              data.Get("api_addr").(string),
		ExposeTargets:        data.Get("expose_targets").(bool),
		TargetMountAllowlist: data.Get("target_mount_allowlist").([]string),
	}

	if err := b.applyConfig(ctx, req, cfg); err != nil {
		return logical.ErrorResponse(err.Error()), nil
	}
	return nil, nil
}
