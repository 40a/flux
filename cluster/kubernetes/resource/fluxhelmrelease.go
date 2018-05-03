package resource

import (
	"fmt"
	"io"

	ifv1 "github.com/weaveworks/flux/apis/helm.integrations.flux.weave.works/v1alpha2"
	"github.com/weaveworks/flux/image"
	"github.com/weaveworks/flux/resource"
	"k8s.io/helm/pkg/chartutil"
)

type FluxHelmRelease struct {
	BaseObject
	Spec ifv1.FluxHelmReleaseSpec
}

// RegisterKind("FluxHelmRelease", ifv1.FluxHelmReleaseSpec, k8sresource.FHRTryUpdate,
// 	k8sresource.FHRUnmarshalKind, k8sresource.FHRGetPodController, k8sresource.FHRUnmarshalKind)

func FHRTryUpdate([]byte, string, image.Ref, io.Writer) error {
	return nil
}

func FHRUnmarshalKind(BaseObject, []byte) (resource.Resource, error) {
	return FluxHelmRelease{}, nil
}

// NOTE commenting out to allow build
//-----------------------------------
// func FHRSomeControllers(*cluster.Cluster, string, string) (podController, error) {
// 	return FluxHelmRelease{}, nil
// }

// func FHRAllControllers(*cluster.Cluster, string) ([]podController, error) {
// 	containers := []resource.Resource{}
// 	return containers, nil
// }

func (fhr FluxHelmRelease) Containers() []resource.Container {
	containers, err := createContainers(fhr)
	if err != nil {
		// log ?
	}
	return containers
}

func createContainers(fhr FluxHelmRelease) ([]resource.Container, error) {
	spec := fhr.Spec

	values := spec.Values
	if len(values) == 0 {
		return nil, nil
	}
	containers := []resource.Container{}

	imgInfo, ok := values["image"]

	// image info appears on the top level, so is associated directly with the chart
	if ok {
		imageRef, err := processImageInfo(values, imgInfo)
		if err != nil {
			return nil, err
		}
		containers = append(containers, resource.Container{Name: spec.ChartGitPath, Image: imageRef})
		return containers, nil
	}

	// no top key is an image parameter =>
	// image is potentially provided nested within the map value of the top key(s)
	for param, value := range values {
		cName, imageRef, err := findImage(spec, param, value)
		if err != nil {
			return nil, err
		}
		if cName != "" {
			containers = append(containers, resource.Container{Name: cName, Image: imageRef})
		}
	}

	return []resource.Container{}, nil
}

func processImageInfo(values map[string]interface{}, value interface{}) (image.Ref, error) {
	var ref image.Ref
	var err error

	switch value.(type) {
	case string:
		val := value.(string)
		ref, err = processImageString(values, val)
		if err != nil {
			return image.Ref{}, err
		}
		return ref, nil

	case map[string]string:
		// image:
		// 			registry: docker.io   (sometimes missing)
		// 			repository: bitnami/mariadb
		// 			tag: 10.1.32					(sometimes version)
		val := value.(map[string]string)
		ref, err = processImageMap(val)
		if err != nil {
			return image.Ref{}, err
		}
		return ref, nil

	default:
		return image.Ref{}, image.ErrMalformedImageID
	}
}

func findImage(spec ifv1.FluxHelmReleaseSpec, param string, value interface{}) (string, image.Ref, error) {
	var ref image.Ref
	var err error
	values := spec.Values

	if param == "image" {
		switch value.(type) {
		case string:
			val := value.(string)
			ref, err = processImageString(values, val)
			if err != nil {
				return "", image.Ref{}, err
			}
			return spec.ChartGitPath, ref, nil

		case map[string]string:
			// image:
			// 			registry: docker.io   (sometimes missing)
			// 			repository: bitnami/mariadb
			// 			tag: 10.1.32					(sometimes version)
			val := value.(map[string]string)

			ref, err = processImageMap(val)
			if err != nil {
				return "", image.Ref{}, err
			}
			return spec.ChartGitPath, ref, nil

		// ???
		default:
			return "", image.Ref{}, image.ErrMalformedImageID
		}
	}

	switch value.(type) {
	case map[string]interface{}:
		// image information is nested ---------------------------------------------------
		// 		controller:
		// 			image:
		// 				repository: quay.io/kubernetes-ingress-controller/nginx-ingress-controller
		// 				tag: "0.12.0"

		// 		jupyter:
		// 			image:
		// 				repository: "daskdev/dask-notebook"
		// 				tag: "0.17.1"

		// 		zeppelin:
		// 			image: dylanmei/zeppelin:0.7.2

		// 		artifactory:
		//   		name: artifactory
		//  	  replicaCount: 1
		//  		image:
		//   		  repository: "docker.bintray.io/jfrog/artifactory-pro"
		//  		  version: 5.9.1
		//   		  pullPolicy: IfNotPresent
		val := value.(map[string]interface{})

		var cName string
		//var ok bool
		if cn, ok := val["name"]; !ok {
			cName = cn.(string)
		}

		refP, err := processMaybeImageMap(val)
		if err != nil {
			return "", image.Ref{}, err
		}
		return cName, *refP, nil

	default:
		return "", image.Ref{}, nil
	}
}

func processImageString(values chartutil.Values, val string) (image.Ref, error) {
	if t, ok := values["imageTag"]; ok {
		val = fmt.Sprintf("%s:%s", val, t)
	} else if t, ok := values["tag"]; ok {
		val = fmt.Sprintf("%s:%s", val, t)
	}
	ref, err := image.ParseRef(val)
	if err != nil {
		return image.Ref{}, err
	}
	// returning chart to be the container name
	return ref, nil
}

func processImageMap(val map[string]string) (image.Ref, error) {
	var ref image.Ref
	var err error

	i, iOk := val["repository"]
	if !iOk {
		return image.Ref{}, image.ErrMalformedImageID
	}

	d, dOk := val["registry"]
	t, tOk := val["tag"]

	if !dOk {
		if tOk {
			i = fmt.Sprintf("%s:%s", i, t)
		}
		ref, err = image.ParseRef(i)
		if err != nil {
			return image.Ref{}, err
		}
		return ref, nil
	}
	if !tOk {
		if dOk {
			i = fmt.Sprintf("%s/%s", d, i)
		}
		ref, err = image.ParseRef(i)
		if err != nil {
			return image.Ref{}, err
		}
		return ref, nil
	}

	name := image.Name{Domain: d, Image: i}
	return image.Ref{Name: name, Tag: t}, nil
}

// processMaybeImageMap processes value of the image parameter, if it exists
func processMaybeImageMap(value map[string]interface{}) (*image.Ref, error) {
	iVal, ok := value["image"]
	if !ok {
		return nil, nil
	}

	var ref image.Ref
	var err error
	switch iVal.(type) {
	case string:
		val := iVal.(string)
		ref, err = processImageString(value, val)
		if err != nil {
			return nil, err
		}
		return &ref, nil

	case map[string]string:
		// image:
		// 			registry: docker.io   (sometimes missing)
		// 			repository: bitnami/mariadb
		// 			tag: 10.1.32					(sometimes version)
		val := iVal.(map[string]string)

		ref, err = processImageMap(val)
		if err != nil {
			return nil, err
		}
		return &ref, nil
	default:
		return nil, nil
	}
}

func createImageRef(domain, imageName, tag string) image.Ref {
	return image.Ref{
		Name: image.Name{
			Domain: domain,
			Image:  imageName,
		},
		Tag: tag,
	}
}
