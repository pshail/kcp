/*
Copyright 2022 The KCP Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internalapis

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/errors"
	endpointsopenapi "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/util/openapi"
	"k8s.io/kube-openapi/pkg/builder"
	"k8s.io/kube-openapi/pkg/common"
	"k8s.io/kube-openapi/pkg/util"

	apisv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/apis/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/crdpuller"
)

// InternalAPI describes an API to be imported from some schemes and generated OpenAPI V2 definitions
type InternalAPI struct {
	Names        apiextensionsv1.CustomResourceDefinitionNames
	GroupVersion schema.GroupVersion
	Instance     runtime.Object
	ResourceSope apiextensionsv1.ResourceScope
	HasStatus    bool
}

// KCPInternalAPIs provides a list of InternalAPI for the APIs that are part of the KCP scheme and will be there in every KCP workspace
var KCPInternalAPIs = []InternalAPI{
	{
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Plural:   "namespaces",
			Singular: "namespace",
			Kind:     "Namespace",
		},
		GroupVersion: schema.GroupVersion{Group: "", Version: "v1"},
		Instance:     &corev1.Namespace{},
		ResourceSope: apiextensionsv1.ClusterScoped,
		HasStatus:    true,
	},
	{
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Plural:   "configmaps",
			Singular: "configmap",
			Kind:     "ConfigMap",
		},
		GroupVersion: schema.GroupVersion{Group: "", Version: "v1"},
		Instance:     &corev1.ConfigMap{},
		ResourceSope: apiextensionsv1.NamespaceScoped,
	},
	{
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Plural:   "secrets",
			Singular: "secret",
			Kind:     "Secret",
		},
		GroupVersion: schema.GroupVersion{Group: "", Version: "v1"},
		Instance:     &corev1.Secret{},
		ResourceSope: apiextensionsv1.NamespaceScoped,
	},
	{
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Plural:   "serviceaccounts",
			Singular: "serviceaccount",
			Kind:     "ServiceAccount",
		},
		GroupVersion: schema.GroupVersion{Group: "", Version: "v1"},
		Instance:     &corev1.ServiceAccount{},
		ResourceSope: apiextensionsv1.NamespaceScoped,
	},
}

func createAPIResourceSchemas(schemes []*runtime.Scheme, openAPIDefinitionsGetters []common.GetOpenAPIDefinitions, defs ...InternalAPI) ([]*apisv1alpha1.APIResourceSchema, error) {
	config := genericapiserver.DefaultOpenAPIConfig(func(ref common.ReferenceCallback) map[string]common.OpenAPIDefinition {
		result := make(map[string]common.OpenAPIDefinition)

		for _, openAPIDefinitionsGetter := range openAPIDefinitionsGetters {
			for key, val := range openAPIDefinitionsGetter(ref) {
				result[key] = val
			}
		}

		return result
	}, endpointsopenapi.NewDefinitionNamer(schemes...))

	var canonicalTypeNames []string
	for _, def := range defs {
		canonicalTypeNames = append(canonicalTypeNames, util.GetCanonicalTypeName(def.Instance))
	}
	swagger, err := builder.BuildOpenAPIDefinitionsForResources(config, canonicalTypeNames...)
	if err != nil {
		return nil, err
	}
	swagger.Info.Version = "1.0"
	models, err := openapi.ToProtoModels(swagger)
	if err != nil {
		return nil, err
	}

	modelsByGKV, err := endpointsopenapi.GetModelsByGKV(models)
	if err != nil {
		return nil, err
	}

	var apis []*apisv1alpha1.APIResourceSchema
	for _, def := range defs {
		gvk := def.GroupVersion.WithKind(def.Names.Kind)
		var schemaProps apiextensionsv1.JSONSchemaProps
		errs := crdpuller.Convert(modelsByGKV[gvk], &schemaProps)
		if len(errs) > 0 {
			return nil, errors.NewAggregate(errs)
		}
		group := def.GroupVersion.Group
		if group == "" {
			group = "core"
		}
		spec := &apisv1alpha1.APIResourceSchema{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("internal.%s.%s", def.Names.Plural, group),
			},
			Spec: apisv1alpha1.APIResourceSchemaSpec{
				Group: def.GroupVersion.Group,
				Names: def.Names,
				Scope: def.ResourceSope,
				Versions: []apisv1alpha1.APIResourceVersion{
					{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema:  runtime.RawExtension{},
					},
				},
			},
		}
		if def.HasStatus {
			spec.Spec.Versions[0].Subresources.Status = &apiextensionsv1.CustomResourceSubresourceStatus{}
		}
		if err := spec.Spec.Versions[0].SetSchema(&schemaProps); err != nil {
			return nil, err
		}

		apis = append(apis, spec)
	}
	return apis, nil
}
