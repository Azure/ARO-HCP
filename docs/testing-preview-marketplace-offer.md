# Azure Marketplace Images for ARO-HCP

This document covers the use of custom Azure Marketplace images for ARO-HCP node pools, and how to deploy and validate a preview (hidden) Azure Marketplace VM image such as an RHCOS image published under the `azureopenshift` publisher.

## Image coordinates and naming

An image is identified by four fields, which together form a URN in the format `publisher:offer:sku:version`:

- `publisher` — the organization that created the image
- `offer` — a group of related images created by the publisher
- `sku` — an instance of an offer (e.g. a major release of a distribution)
- `version` — the version of the image SKU

For example:

```
azureopenshift:aro4:aro_5-0_x64_gen2:10.2.20260423
```

For the ARO images themselves the coordinates follow the installer's conventions
([openshift/installer#10764](https://github.com/openshift/installer/pull/10764)):
publisher `azureopenshift`, offer `aro4`, and from OpenShift 5.0 onwards gen2-only SKUs
named `aro_<major>-<minor>_<arch>_gen2`. The version's major segment is the RHEL major
version, so `10.2.20260423` is a RHEL 10 image and `9.8.20260428` a RHEL 9 one. Releases
before 5.0 keep the older `aro_<xy>`/`aro_<xy>-v2`/`aro_<xy>-arm` naming.

The ARO 5.0 images currently published under `azureopenshift:aro4`:

| Architecture | SKU | RHEL 9 version | RHEL 10 version |
|---|---|---|---|
| x64 | `aro_5-0_x64_gen2` | `9.8.20260428` | `10.2.20260423` |
| ARM64 | `aro_5-0_arm_gen2` | `9.8.20260428` | `10.2.20260423` |

List the current set at any time with:

```bash
az vm image list --publisher azureopenshift --offer aro4 \
  --sku aro_5-0_x64_gen2 --location westus3 --all -o table
```

To discover every published SKU under the offer:

```bash
az vm image list-skus --publisher azureopenshift --offer aro4 --location westus3 -o table
```

## Using a marketplace image on a node pool

Set the experimental ARM tag `aro-hcp.experimental.nodepool.marketplace-image` on the node
pool to the image URN. The tag requires the `ExperimentalReleaseFeatures` AFEC to be
registered on the subscription; without it the tag is ignored and the node pool gets the
default RHCOS image.

```
aro-hcp.experimental.nodepool.marketplace-image = azureopenshift:aro4:aro_5-0_x64_gen2:10.2.20260423
```

The frontend parses the URN and forwards it to Clusters Service as the `image` field on
`azure_node_pool`, which maps onto the Hypershift API structure of the same four fields.

### Validation

The following validations are performed:

1. **Synchronous (at request time):** All four fields must be provided together, and
   each must not exceed its maximum length:
   - `publisher` — at most 128 characters
   - `offer` — at most 64 characters
   - `sku` — at most 64 characters
   - `version` — at most 32 characters

   The publisher/offer/sku limits are the [published Azure Compute Gallery image
   definition identifier limits](https://learn.microsoft.com/en-us/azure/virtual-machines/troubleshooting-shared-images#creating-or-modifying-image-definitions)
   (128/64/64), which Azure recommends matching to the originating Marketplace image.
   Azure does not publish a character limit for `version`; the
   [documented format](https://learn.microsoft.com/en-us/azure/virtual-machines/image-version#prerequisites)
   is `Major.Minor.Patch` with each segment a 32-bit integer, from which the 32-character
   bound is derived (three 10-digit segments plus two separators).
2. **Inflight (async via worker):**
   - The image must exist and be accessible in the cluster's Azure region.
   - The image architecture must match the VM size architecture (ARM64 vs x64).

The image is immutable once the node pool exists: a PATCH carrying
`azure_node_pool.image` is rejected with `Attribute 'azure_node_pool.image' is not
allowed`.

## Testing a preview (hidden) offer

Azure Marketplace offers can be published in a **preview** state before going live. Preview images are only visible to Azure subscriptions that have been added to the offer's **preview audience** in [Partner Center](https://partner.microsoft.com/dashboard/home). For full details, see:

- [Add a preview audience for a VM offer](https://learn.microsoft.com/en-us/partner-center/marketplace-offers/azure-vm-preview-audience)
- [How do I test a hidden preview image?](https://learn.microsoft.com/en-us/partner-center/marketplace-offers/azure-vm-faq#how-do-i-test-a-hidden-preview-image-)

### Prerequisites

1. Your Azure subscription must be in the offer's **preview audience** (configured in Partner Center). If it is not, the deployment will fail with a `NotFound` error.
2. The ARO marketplace image (`azureopenshift:aro4`) is a **CoreVM** offering, so no `plan` block is required in the ARM/Bicep template. However, the `-preview` suffix on the offer ID **is still required**.

### 1. Deploy via Bicep/ARM template

For preview images, append `-preview` to the offer ID in the `imageReference`. Direct CLI deployment (`az vm create`) does **not** work for hidden preview images — you must use an ARM template deployment.

Example `imageReference` for a preview image:

```bicep
storageProfile: {
  imageReference: {
    publisher: 'azureopenshift'
    offer: 'aro4-preview'       // append '-preview' for hidden preview images
    sku: 'aro_5-0_x64_gen2'
    version: '10.2.20260423'
  }
}
```

For a live (non-preview) image, use the offer ID without the suffix:

```bicep
storageProfile: {
  imageReference: {
    publisher: 'azureopenshift'
    offer: 'aro4'
    sku: 'aro_5-0_x64_gen2'
    version: '10.2.20260423'
  }
}
```

### 2. Deploy

A sample Bicep template that creates a self-contained VM with networking and boot diagnostics is available at `dev-infrastructure/templates/test-marketplace-vm.bicep` (or can be created from this example).

```bash
# Create a resource group
az group create --name aro-marketplace-test --location eastus

# Deploy the preview image
az deployment group create \
  --resource-group aro-marketplace-test \
  --template-file <path-to-bicep> \
  --parameters sshPublicKey="$(cat ~/.ssh/id_rsa.pub)" isPreview=true
```

### 3. Validate the VM booted correctly

Check the boot diagnostics to confirm the RHCOS image provisioned successfully:

```bash
az vm boot-diagnostics get-boot-log \
  --resource-group aro-marketplace-test \
  --name aro-rhcos-vm
```

You can also view the boot screenshot and serial console output in the Azure portal under **VM > Boot diagnostics**.

### 4. Clean up

```bash
az group delete --name aro-marketplace-test --yes --no-wait
```

### Key differences: preview vs. live images

| Aspect | Preview image | Live image |
|--------|--------------|------------|
| Offer ID in `imageReference` | `aro4-preview` | `aro4` |
| Subscription requirement | Must be in preview audience | Publicly available |
| Deployment method | ARM/Bicep template only | ARM/Bicep or `az vm create` |
| Plan block (CoreVM offers) | Not required | Not required |
| Marketplace terms acceptance | Not required (CoreVM) | Not required (CoreVM) |

## References

- [Azure VM FAQ — Testing Preview Audience Images](https://learn.microsoft.com/en-us/partner-center/marketplace-offers/azure-vm-faq)
- [Image definition identifier limits (publisher/offer/sku)](https://learn.microsoft.com/en-us/azure/virtual-machines/troubleshooting-shared-images#creating-or-modifying-image-definitions)
- [Image version format](https://learn.microsoft.com/en-us/azure/virtual-machines/image-version#prerequisites)
- [Product Ingestion API — VM](https://learn.microsoft.com/en-us/partner-center/marketplace-offers/product-ingestion-api-vm)
- [SFI Certificate and Secret Management](https://aka.ms/certandsecretmanagement)
