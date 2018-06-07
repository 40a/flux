/*

This package has a component for monitoring the Helm releases under
our control, and annotating the resources involved. Specifically:

 - updating the FluxHelmRelease status based on the state of the
   associated release; and,

 - marking each resource that results from a FluxHelmRelease (via
   Helm) as belonging to the FluxHelmRelease.

*/
package annotator

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-kit/kit/log"
	"golang.org/x/time/rate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kube "k8s.io/client-go/kubernetes"
	"k8s.io/helm/pkg/helm"

	fluxhelmtypes "github.com/weaveworks/flux/apis/helm.integrations.flux.weave.works/v1alpha2"
	fluxhelm "github.com/weaveworks/flux/integrations/client/clientset/versioned"
	"github.com/weaveworks/flux/integrations/helm/release"
)

type Annotator struct {
	rateLimiter *rate.Limiter
	fluxhelm    fluxhelm.Interface
	kube        kube.Interface
	helmClient  *helm.Client
}

func New(fhrClient fluxhelm.Interface, kubeClient kube.Interface, helmClient *helm.Client) *Annotator {
	limiter := rate.NewLimiter(rate.Every(5*time.Second), 5) // arbitrary
	return &Annotator{
		rateLimiter: limiter,
		fluxhelm:    fhrClient,
		kube:        kubeClient,
		helmClient:  helmClient,
	}
}

func (a *Annotator) Loop(stop <-chan struct{}, logger log.Logger) {
	ctx := context.Background()
	var logErr error

bail:
	for {
		select {
		case <-stop:
			break bail
		default:
		}

		if err := a.rateLimiter.Wait(ctx); err != nil {
			logErr = err
			break bail
		}

		// Look up FluxHelmReleases
		namespaces, err := a.kube.CoreV1().Namespaces().List(metav1.ListOptions{})
		if err != nil {
			logErr = err
			break bail
		}
		for _, ns := range namespaces.Items {
			fhrIf := a.fluxhelm.HelmV1alpha2().FluxHelmReleases(ns.Name)
			fhrs, err := fhrIf.List(metav1.ListOptions{})
			if err != nil {
				logErr = err
				break bail
			}
			for _, fhr := range fhrs.Items {
				releaseName := release.GetReleaseName(fhr)
				println("[DEBUG] Looking at FHR", fhr.Name, " -> release named", releaseName)
				content, err := a.helmClient.ReleaseContent(releaseName)
				if err != nil {
					logger.Log("err", err)
					continue
				}
				status := content.GetRelease().GetInfo().GetStatus()
				println("[DEBUG]", releaseName, status.GetCode().String())
				if status.GetCode().String() != fhr.Status.ReleaseStatus {
					newStatus := fluxhelmtypes.FluxHelmReleaseStatus{
						ReleaseStatus: status.GetCode().String(),
					}
					var patchBytes []byte
					if patchBytes, err = json.Marshal(map[string]interface{}{
						"status": newStatus,
					}); err == nil {
						// CustomResources don't get
						// StrategicMergePatch, for now, but since we
						// want to unconditionally set the value, this
						// is OK.
						_, err = fhrIf.Patch(fhr.Name, types.MergePatchType, patchBytes)
					}
					if err != nil {
						logger.Log("namespace", ns.Name, "resource", fhr.Name, "err", err)
						continue
					}
				}
			}
		}

		// Get the corresponding HelmRelease status
		// Annotate/Status things
		// (will probably need to keep a worklist)
	}

	logger.Log("loop", "stopping", "err", logErr)
}
