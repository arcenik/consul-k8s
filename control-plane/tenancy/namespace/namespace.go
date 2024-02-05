// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package namespace

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/hashicorp/consul-k8s/control-plane/api/common"
	"github.com/hashicorp/consul/proto-public/pbresource"
	pbtenancy "github.com/hashicorp/consul/proto-public/pbtenancy/v2beta1"
)

// DeletionTimestampKey is the key in a resource's metadata that stores the timestamp
// when a resource was marked for deletion. This only applies to resources with finalizers.
const DeletionTimestampKey = "deletionTimestamp"

// EnsureDeleted ensures a Consul namespace with name ns in partition ap is deleted or is in the
// process of being deleted. If neither, it will mark it for deletion.
func EnsureDeleted(ctx context.Context, client pbresource.ResourceServiceClient, ap, ns string) error {
	if ns == common.WildcardNamespace || ns == common.DefaultNamespaceName {
		return nil
	}

	// Check if the Consul namespace exists.
	rsp, err := client.Read(ctx, &pbresource.ReadRequest{Id: &pbresource.ID{
		Name:    ns,
		Type:    pbtenancy.NamespaceType,
		Tenancy: &pbresource.Tenancy{Partition: ap},
	}})

	switch {
	case status.Code(err) == codes.NotFound:
		// Nothing to do
		return nil
	case err != nil:
		// Unexpected error
		return fmt.Errorf("namespace read failed: %w", err)
	case isMarkedForDeletion(rsp.Resource):
		// Deletion already in progress, nothing to do
		return nil
	default:
		// Namespace found, so non-CAS delete it.
		_, err = client.Delete(ctx, &pbresource.DeleteRequest{Id: rsp.Resource.Id, Version: ""})
		if err != nil {
			return fmt.Errorf("namespace delete failed: %w", err)
		}
		return nil
	}
}

// EnsureExists ensures a Consul namespace with name ns exists and is not marked
// for deletion. If it doesn't, exist it will create it. If it is marked for deletion,
// returns an error.
//
// Boolean return value indicates if the namespace was created by this call.
func EnsureExists(ctx context.Context, client pbresource.ResourceServiceClient, ap, ns string) (bool, error) {
	if ns == common.WildcardNamespace || ns == common.DefaultNamespaceName {
		return false, nil
	}

	// Check if the Consul namespace exists.
	rsp, err := client.Read(ctx, &pbresource.ReadRequest{Id: &pbresource.ID{
		Name:    ns,
		Type:    pbtenancy.NamespaceType,
		Tenancy: &pbresource.Tenancy{Partition: ap},
	}})

	switch {
	case err == nil && isMarkedForDeletion(rsp.Resource):
		// Found, but delete in progress
		return false, fmt.Errorf("consul namespace %q deletion in progress", ns)
	case err == nil:
		// Found and not marked for deletion, nothing to do
		return false, nil
	case status.Code(err) != codes.NotFound:
		// Unexpected error
		return false, fmt.Errorf("consul namespace read failed: %w", err)
	}

	// Consul namespace not found, so create it
	// TODO: Handle creation of crossNSACLPolicy when V2 ACLs are supported
	nsData, err := anypb.New(&pbtenancy.Namespace{Description: "Auto-generated by consul-k8s"})
	if err != nil {
		return false, err
	}

	_, err = client.Write(ctx, &pbresource.WriteRequest{Resource: &pbresource.Resource{
		Id: &pbresource.ID{
			Name:    ns,
			Type:    pbtenancy.NamespaceType,
			Tenancy: &pbresource.Tenancy{Partition: ap},
		},
		Metadata: map[string]string{"external-source": "kubernetes"},
		Data:     nsData,
	}})

	if err != nil {
		return false, fmt.Errorf("consul namespace creation failed: %w", err)
	}
	return true, nil
}

// isMarkedForDeletion returns true if a resource has been marked for deletion,
// false otherwise.
func isMarkedForDeletion(res *pbresource.Resource) bool {
	if res.Metadata == nil {
		return false
	}
	_, ok := res.Metadata[DeletionTimestampKey]
	return ok
}
