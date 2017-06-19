package common

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kapi "k8s.io/kubernetes/pkg/api"

	buildapi "github.com/openshift/origin/pkg/build/api"
	buildclient "github.com/openshift/origin/pkg/build/client"
	buildutil "github.com/openshift/origin/pkg/build/util"
	"github.com/openshift/origin/pkg/client/testclient"
)

func mockBuildConfig(name string) buildapi.BuildConfig {
	appName := strings.Split(name, "-")
	successfulBuildsToKeep := int32(2)
	failedBuildsToKeep := int32(3)
	return buildapi.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-build", appName[0]),
			Namespace: "namespace",
			Labels: map[string]string{
				"app": appName[0],
			},
		},
		Spec: buildapi.BuildConfigSpec{
			SuccessfulBuildsHistoryLimit: &successfulBuildsToKeep,
			FailedBuildsHistoryLimit:     &failedBuildsToKeep,
		},
	}
}

func mockBuild(name string, phase buildapi.BuildPhase, stamp *metav1.Time) buildapi.Build {
	appName := strings.Split(name, "-")
	return buildapi.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			UID:               types.UID(fmt.Sprintf("uid%v", appName[1])),
			Namespace:         "namespace",
			CreationTimestamp: *stamp,
			Labels: map[string]string{
				"app": appName[0],
				buildapi.BuildConfigLabel: fmt.Sprintf("%v-build", appName[0]),
				"buildconfig":             fmt.Sprintf("%v-build", appName[0]),
			},
			Annotations: map[string]string{
				buildapi.BuildConfigLabel: fmt.Sprintf("%v-build", appName[0]),
			},
		},
		Status: buildapi.BuildStatus{
			Phase:          phase,
			StartTimestamp: stamp,
			Config: &kapi.ObjectReference{
				Name:      fmt.Sprintf("%v-build", appName[0]),
				Namespace: "namespace",
			},
		},
	}
}

func mockBuildsList(length int) (buildapi.BuildConfig, []buildapi.Build) {
	var builds []buildapi.Build
	buildPhaseList := []buildapi.BuildPhase{buildapi.BuildPhaseComplete, buildapi.BuildPhaseFailed}
	addOrSubtract := []string{"+", "-"}

	for i := 0; i < length; i++ {
		duration, _ := time.ParseDuration(fmt.Sprintf("%v%vh", addOrSubtract[i%2], i))
		startTime := metav1.NewTime(time.Now().Add(duration))
		build := mockBuild(fmt.Sprintf("myapp-%v", i), buildPhaseList[i%2], &startTime)
		builds = append(builds, build)
	}

	return mockBuildConfig("myapp"), builds
}

func TestSetBuildCompletionTimeAndDuration(t *testing.T) {
	build := &buildapi.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-1",
			Namespace: "namespace",
		},
	}

	now := metav1.Now()
	SetBuildCompletionTimeAndDuration(build, &now)

	if build.Status.StartTimestamp == nil {
		t.Errorf("should have set the StartTimestamp, but instead it was nil")
	}
	if build.Status.CompletionTimestamp == nil {
		t.Errorf("should have set the CompletionTimestamp, but instead it was nil")
	}
	if build.Status.Duration > 0 {
		t.Errorf("should have set the Duration to 0s, but instead it was %v", build.Status.Duration)
	}
}

func TestHandleBuildPruning(t *testing.T) {
	var objects []runtime.Object
	buildconfig, builds := mockBuildsList(10)

	objects = append(objects, &buildconfig)
	for index := range builds {
		objects = append(objects, &builds[index])
	}

	osclient := testclient.NewSimpleFake(objects...)

	build, err := osclient.Builds("namespace").Get("myapp-0", metav1.GetOptions{})
	if err != nil {
		t.Errorf("%v", err)
	}

	buildLister := buildclient.NewOSClientBuildClient(osclient)
	buildConfigGetter := buildclient.NewOSClientBuildConfigClient(osclient)
	buildDeleter := buildclient.NewBuildDeleter(osclient)

	bcName := buildutil.ConfigNameForBuild(build)
	successfulStartingBuilds, err := buildutil.BuildConfigBuilds(buildLister, build.Namespace, bcName, func(build buildapi.Build) bool { return build.Status.Phase == buildapi.BuildPhaseComplete })
	sort.Sort(ByCreationTimestamp(successfulStartingBuilds.Items))

	failedStartingBuilds, err := buildutil.BuildConfigBuilds(buildLister, build.Namespace, bcName, func(build buildapi.Build) bool { return build.Status.Phase == buildapi.BuildPhaseFailed })
	sort.Sort(ByCreationTimestamp(failedStartingBuilds.Items))

	if len(successfulStartingBuilds.Items)+len(failedStartingBuilds.Items) != 10 {
		t.Errorf("should start with 10 builds, but started with %v instead", len(successfulStartingBuilds.Items)+len(failedStartingBuilds.Items))
	}

	if err := HandleBuildPruning(bcName, build.Namespace, buildLister, buildConfigGetter, buildDeleter); err != nil {
		t.Errorf("error pruning builds: %v", err)
	}

	successfulRemainingBuilds, err := buildutil.BuildConfigBuilds(buildLister, build.Namespace, bcName, func(build buildapi.Build) bool { return build.Status.Phase == buildapi.BuildPhaseComplete })
	sort.Sort(ByCreationTimestamp(successfulRemainingBuilds.Items))

	failedRemainingBuilds, err := buildutil.BuildConfigBuilds(buildLister, build.Namespace, bcName, func(build buildapi.Build) bool { return build.Status.Phase == buildapi.BuildPhaseFailed })
	sort.Sort(ByCreationTimestamp(failedRemainingBuilds.Items))

	if len(successfulRemainingBuilds.Items)+len(failedRemainingBuilds.Items) != 5 {
		t.Errorf("there should only be 5 builds left, but instead there are %v", len(successfulRemainingBuilds.Items)+len(failedRemainingBuilds.Items))
	}

	if !reflect.DeepEqual(successfulStartingBuilds.Items[:2], successfulRemainingBuilds.Items) {
		t.Errorf("expected the two most recent successful builds should be left, but instead there were %v: %v", len(successfulRemainingBuilds.Items), successfulRemainingBuilds.Items)
	}

	if !reflect.DeepEqual(failedStartingBuilds.Items[:3], failedRemainingBuilds.Items) {
		t.Errorf("expected the three most recent failed builds to be left, but instead there were %v: %v", len(failedRemainingBuilds.Items), failedRemainingBuilds.Items)
	}

}
