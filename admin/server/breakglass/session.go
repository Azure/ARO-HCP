//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package breakglass

import (
	"context"

	time "time"

	sessiongateapiv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	clientset "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned"
	sessiongateclientv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/typed/sessiongate/v1alpha1"
	informers "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/informers/externalversions"
	sessiongatelisterv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SessionReader interface {
	GetSession(ctx context.Context, name string) (*sessiongateapiv1alpha1.Session, error)
}

type SessionWriter interface {
	CreateSession(ctx context.Context, sessionSpec sessiongateapiv1alpha1.SessionSpec) (*sessiongateapiv1alpha1.Session, error)
}

type SessionInterface struct {
	sessionClient    sessiongateclientv1alpha1.SessiongateV1alpha1Interface
	sessionLister    sessiongatelisterv1alpha1.SessionNamespaceLister
	sessionNamespace string
}

func NewSessionInterface(ctx context.Context, sessiongateClientSet clientset.Interface, sessionNamespace string) *SessionInterface {
	sessiongateInformers := informers.NewSharedInformerFactoryWithOptions(
		sessiongateClientSet,
		time.Second*300,
		informers.WithNamespace(sessionNamespace),
	)
	sessionLister := sessiongateInformers.Sessiongate().V1alpha1().Sessions().Lister().Sessions(sessionNamespace)
	go sessiongateInformers.Start(ctx.Done())

	return &SessionInterface{
		sessionClient:    sessiongateClientSet.SessiongateV1alpha1(),
		sessionLister:    sessionLister,
		sessionNamespace: sessionNamespace,
	}
}

func (s *SessionInterface) CreateSession(ctx context.Context, sessionSpec sessiongateapiv1alpha1.SessionSpec) (*sessiongateapiv1alpha1.Session, error) {
	session := &sessiongateapiv1alpha1.Session{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "s-",
			Namespace:    s.sessionNamespace,
		},
		Spec: sessionSpec,
	}
	return s.sessionClient.Sessions(s.sessionNamespace).Create(ctx, session, metav1.CreateOptions{})
}

func (s *SessionInterface) GetSession(ctx context.Context, name string) (*sessiongateapiv1alpha1.Session, error) {
	return s.sessionLister.Get(name)
}
