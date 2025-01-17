// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package vault

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/sdk/helper/certutil"

	"github.com/hashicorp/terraform-provider-vault/internal/consts"
	"github.com/hashicorp/terraform-provider-vault/internal/provider"
	"github.com/hashicorp/terraform-provider-vault/util"
)

func pkiSecretBackendRootCertResource() *schema.Resource {
	return &schema.Resource{
		CreateContext: pkiSecretBackendRootCertCreate,
		DeleteContext: pkiSecretBackendRootCertDelete,
		UpdateContext: func(ctx context.Context, data *schema.ResourceData, i interface{}) diag.Diagnostics {
			return nil
		},
		ReadContext: provider.ReadContextWrapper(pkiSecretBackendCertRead),
		StateUpgraders: []schema.StateUpgrader{
			{
				Version: 0,
				Type:    pkiSecretSerialNumberResourceV0().CoreConfigSchema().ImpliedType(),
				Upgrade: pkiSecretSerialNumberUpgradeV0,
			},
		},
		SchemaVersion: 1,
		CustomizeDiff: func(_ context.Context, d *schema.ResourceDiff, meta interface{}) error {
			key := consts.FieldSerial
			o, _ := d.GetChange(key)
			// skip on new resource
			if o.(string) == "" {
				return nil
			}

			client, e := provider.GetClient(d, meta)
			if e != nil {
				return e
			}

			cert, err := getCACertificate(client, d.Get(consts.FieldBackend).(string))
			if err != nil {
				return err
			}

			if cert != nil {
				n := certutil.GetHexFormatted(cert.SerialNumber.Bytes(), ":")
				if d.Get(key).(string) != n {
					if err := d.SetNewComputed(key); err != nil {
						return err
					}
					if err := d.ForceNew(key); err != nil {
						return err
					}
				}

			}

			return nil
		},

		Schema: map[string]*schema.Schema{
			consts.FieldBackend: {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The PKI secret backend the resource belongs to.",
				ForceNew:    true,
			},
			consts.FieldType: {
				Type:         schema.TypeString,
				Required:     true,
				Description:  "Type of root to create. Must be either \"existing\", \"exported\", \"internal\" or \"kms\"",
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice([]string{consts.FieldExisting, consts.FieldExported, consts.FieldInternal, keyTypeKMS}, false),
			},
			consts.FieldCommonName: {
				Type:        schema.TypeString,
				Required:    true,
				Description: "CN of root to create.",
				ForceNew:    true,
			},
			consts.FieldAltNames: {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "List of alternative names.",
				ForceNew:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			consts.FieldIPSans: {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "List of alternative IPs.",
				ForceNew:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			consts.FieldURISans: {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "List of alternative URIs.",
				ForceNew:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			consts.FieldOtherSans: {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "List of other SANs.",
				ForceNew:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			consts.FieldTTL: {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    false,
				Description: "Time to live.",
			},
			consts.FieldFormat: {
				Type:         schema.TypeString,
				Optional:     true,
				Description:  "The format of data.",
				ForceNew:     true,
				Default:      "pem",
				ValidateFunc: validation.StringInSlice([]string{"pem", "der", "pem_bundle"}, false),
			},
			consts.FieldPrivateKeyFormat: {
				Type:         schema.TypeString,
				Optional:     true,
				Description:  "The private key format.",
				ForceNew:     true,
				Default:      "der",
				ValidateFunc: validation.StringInSlice([]string{"der", "pkcs8"}, false),
			},
			consts.FieldKeyType: {
				Type:         schema.TypeString,
				Optional:     true,
				Description:  "The desired key type.",
				ForceNew:     true,
				Default:      "rsa",
				ValidateFunc: validation.StringInSlice([]string{"rsa", "ec", "ed25519"}, false),
			},
			consts.FieldKeyBits: {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "The number of bits to use.",
				ForceNew:    true,
				Default:     2048,
			},
			consts.FieldMaxPathLength: {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "The maximum path length to encode in the generated certificate.",
				ForceNew:    true,
				Default:     -1,
			},
			consts.FieldExcludeCNFromSans: {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Flag to exclude CN from SANs.",
				ForceNew:    true,
			},
			consts.FieldPermittedDNSDomains: {
				Type:        schema.TypeList,
				Optional:    true,
				Description: "List of domains for which certificates are allowed to be issued.",
				ForceNew:    true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			consts.FieldOu: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The organization unit.",
				ForceNew:    true,
			},
			consts.FieldOrganization: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The organization.",
				ForceNew:    true,
			},
			consts.FieldCountry: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The country.",
				ForceNew:    true,
			},
			consts.FieldLocality: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The locality.",
				ForceNew:    true,
			},
			consts.FieldProvince: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The province.",
				ForceNew:    true,
			},
			consts.FieldStreetAddress: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The street address.",
				ForceNew:    true,
			},
			consts.FieldPostalCode: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The postal code.",
				ForceNew:    true,
			},
			consts.FieldCertificate: {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The certificate.",
			},
			consts.FieldIssuingCA: {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The issuing CA.",
			},
			consts.FieldSerial: {
				Type:        schema.TypeString,
				Computed:    true,
				Deprecated:  "Use serial_number instead",
				Description: "The serial number.",
			},
			consts.FieldSerialNumber: {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The certificate's serial number, hex formatted.",
			},
			consts.FieldManagedKeyName: {
				Type:          schema.TypeString,
				Optional:      true,
				Computed:      true,
				Description:   "The name of the previously configured managed key.",
				ForceNew:      true,
				ConflictsWith: []string{consts.FieldManagedKeyID},
			},
			consts.FieldManagedKeyID: {
				Type:          schema.TypeString,
				Optional:      true,
				Computed:      true,
				Description:   "The ID of the previously configured managed key.",
				ForceNew:      true,
				ConflictsWith: []string{consts.FieldManagedKeyName},
			},
			consts.FieldIssuerName: {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				Description: "Provides a name to the specified issuer. The name must be unique " +
					"across all issuers and not be the reserved value 'default'.",
				ForceNew: true,
			},
			consts.FieldIssuerID: {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The ID of the generated issuer.",
				ForceNew:    true,
			},
			consts.FieldKeyName: {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				Description: "When a new key is created with this request, optionally specifies " +
					"the name for this.",
				ForceNew: true,
			},
			consts.FieldKeyRef: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "Specifies the key to use for generating this request.",
				ForceNew:    true,
			},
			consts.FieldKeyID: {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The ID of the generated key.",
				ForceNew:    true,
			},
		},
	}
}

func pkiSecretBackendRootCertCreate(_ context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client, e := provider.GetClient(d, meta)
	if e != nil {
		return diag.FromErr(e)
	}

	backend := d.Get(consts.FieldBackend).(string)
	rootType := d.Get(consts.FieldType).(string)

	path := pkiSecretBackendIntermediateSetSignedReadPath(backend, rootType)

	rootCertAPIFields := []string{
		consts.FieldCommonName,
		consts.FieldTTL,
		consts.FieldFormat,
		consts.FieldPrivateKeyFormat,
		consts.FieldMaxPathLength,
		consts.FieldOu,
		consts.FieldOrganization,
		consts.FieldCountry,
		consts.FieldLocality,
		consts.FieldProvince,
		consts.FieldStreetAddress,
		consts.FieldPostalCode,
		consts.FieldManagedKeyName,
		consts.FieldManagedKeyID,
	}

	rootCertBooleanAPIFields := []string{
		consts.FieldExcludeCNFromSans,
	}

	rootCertStringArrayFields := []string{
		consts.FieldAltNames,
		consts.FieldIPSans,
		consts.FieldURISans,
		consts.FieldOtherSans,
		consts.FieldPermittedDNSDomains,
	}

	// add multi-issuer write API fields if supported
	isIssuerAPISupported := provider.IsAPISupported(meta, provider.VaultVersion111)

	if !(rootType == keyTypeKMS || rootType == consts.FieldExisting) {
		rootCertAPIFields = append(rootCertAPIFields, consts.FieldKeyType, consts.FieldKeyBits)
		if isIssuerAPISupported {
			rootCertAPIFields = append(rootCertAPIFields, consts.FieldKeyRef, consts.FieldIssuerName)
		}
	} else if isIssuerAPISupported {
		rootCertAPIFields = append(rootCertAPIFields, consts.FieldKeyName, consts.FieldIssuerName)
	}

	data := map[string]interface{}{}
	for _, k := range rootCertAPIFields {
		if v, ok := d.GetOk(k); ok {
			data[k] = v
		}
	}

	// add boolean fields
	for _, k := range rootCertBooleanAPIFields {
		data[k] = d.Get(k)
	}

	// add comma separated string fields
	for _, k := range rootCertStringArrayFields {
		m := util.ToStringArray(d.Get(k).([]interface{}))
		if len(m) > 0 {
			data[k] = strings.Join(m, ",")
		}
	}

	log.Printf("[DEBUG] Creating root cert on PKI secret backend %q", backend)
	resp, err := client.Logical().Write(path, data)
	if err != nil {
		return diag.Errorf("error creating root cert for PKI secret backend %q: %s", backend, err)
	}
	log.Printf("[DEBUG] Created root cert on PKI secret backend %q", backend)

	// helpful to consolidate code into single loop
	// since 'serial' is deprecated, we read the 'serial_number'
	// field from the response in order to set to the TF state
	certFieldsMap := map[string]string{
		consts.FieldCertificate:  consts.FieldCertificate,
		consts.FieldIssuingCA:    consts.FieldIssuingCA,
		consts.FieldSerialNumber: consts.FieldSerialNumber,
		consts.FieldSerial:       consts.FieldSerialNumber,
	}

	// multi-issuer API fields that are set to TF state
	// after a read from Vault
	multiIssuerAPIComputedFields := []string{
		consts.FieldIssuerID,
		consts.FieldKeyID,
	}

	if isIssuerAPISupported {
		// add multi-issuer read API fields to field map
		for _, k := range multiIssuerAPIComputedFields {
			certFieldsMap[k] = k
		}
	}

	for k, v := range certFieldsMap {
		if err := d.Set(k, resp.Data[v]); err != nil {
			return diag.FromErr(err)
		}
	}

	d.SetId(path)

	return nil
}

func getCACertificate(client *api.Client, mount string) (*x509.Certificate, error) {
	path := fmt.Sprintf("/v1/%s/ca/pem", mount)
	req := client.NewRequest(http.MethodGet, path)
	req.ClientToken = ""
	resp, err := client.RawRequest(req)
	if err != nil {
		if util.ErrorContainsHTTPCode(err, http.StatusNotFound, http.StatusForbidden) {
			return nil, nil
		}
		return nil, err
	}

	if resp == nil {
		return nil, fmt.Errorf("expected a response body, got nil response")
	}

	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	b, _ := pem.Decode(data)
	if b != nil {
		cert, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, err
		}
		return cert, nil
	}

	return nil, nil
}

func pkiSecretBackendRootCertDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	client, e := provider.GetClient(d, meta)
	if e != nil {
		return diag.FromErr(e)
	}

	backend := d.Get(consts.FieldBackend).(string)

	path := pkiSecretBackendIntermediateSetSignedDeletePath(backend)

	log.Printf("[DEBUG] Deleting root cert from PKI secret backend %q", path)
	if _, err := client.Logical().Delete(path); err != nil {
		return diag.Errorf("error deleting root cert from PKI secret backend %q: %s", path, err)
	}
	log.Printf("[DEBUG] Deleted root cert from PKI secret backend %q", path)
	return nil
}

func pkiSecretBackendIntermediateSetSignedReadPath(backend string, rootType string) string {
	return strings.Trim(backend, "/") + "/root/generate/" + strings.Trim(rootType, "/")
}

func pkiSecretBackendIntermediateSetSignedDeletePath(backend string) string {
	return strings.Trim(backend, "/") + "/root"
}

func pkiSecretSerialNumberResourceV0() *schema.Resource {
	return &schema.Resource{
		Schema: map[string]*schema.Schema{
			consts.FieldSerialNumber: {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
		},
	}
}

func pkiSecretSerialNumberUpgradeV0(
	_ context.Context, rawState map[string]interface{}, _ interface{},
) (map[string]interface{}, error) {
	rawState[consts.FieldSerialNumber] = rawState[consts.FieldSerial]

	return rawState, nil
}
