package kubetarget

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/engine"
	fabkube "github.com/dumbmachine/fabricate/engine/kubernetes"
	fabssh "github.com/dumbmachine/fabricate/engine/ssh"
	"github.com/dumbmachine/fabricate/profile"
	"github.com/dumbmachine/fabricate/target"
	xssh "golang.org/x/crypto/ssh"
	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authentication/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	defaultNamespace = "fabricate"
	managerLabel     = "app.kubernetes.io/managed-by"
	instanceLabel    = "fabricate.dev/instance"
	engineLabel      = "fabricate.dev/engine"
	profileLabel     = "fabricate.dev/profile"
	roleLabel        = "fabricate.dev/role"
)

type kubeClient struct {
	client         kubernetes.Interface
	restConfig     *rest.Config
	contextName    string
	kubeconfigPath string
	namespace      string
}

// Create provisions a fab profile into an existing Kubernetes cluster
// using the caller's local kubeconfig credentials.
func Create(ctx context.Context, name string, p *profile.Profile, info engine.Info, opts target.Options) (*engine.Instance, error) {
	opts, err := target.Normalize(opts)
	if err != nil {
		return nil, err
	}
	if opts.Target != target.Kubernetes {
		return nil, fmt.Errorf("kubetarget create requires target %q", target.Kubernetes)
	}

	kc, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	if err := kc.ensureNamespace(ctx); err != nil {
		return nil, err
	}

	image := engine.ResolveImage(p.Image, info.DefaultImage)
	switch p.Engine {
	case "postgres":
		return kc.createPostgres(ctx, name, p, image)
	case "mysql":
		return kc.createMySQL(ctx, name, p, image)
	case "mongodb":
		return kc.createMongoDB(ctx, name, p, image)
	case "redis":
		return kc.createRedis(ctx, name, p, image)
	case "prometheus":
		return kc.createPrometheus(ctx, name, p, image)
	case "ssh":
		return kc.createSSH(ctx, name, p, image)
	case "kubernetes":
		return kc.createKubernetesResource(ctx, name, p, image)
	default:
		return nil, fmt.Errorf("fab target %q does not support engine %q", target.Kubernetes, p.Engine)
	}
}

// Destroy removes Kubernetes objects created for the instance.
func Destroy(ctx context.Context, inst *engine.Instance) error {
	if inst == nil || inst.Kubernetes == nil {
		return fmt.Errorf("instance has no kubernetes target metadata")
	}
	if len(inst.Kubernetes.Labels) == 0 {
		return fmt.Errorf("instance %q has no kubernetes labels for cleanup", inst.Name)
	}
	kc, err := newClient(target.Options{
		Target:      target.Kubernetes,
		KubeContext: inst.Kubernetes.Context,
		Kubeconfig:  inst.Kubernetes.KubeconfigPath,
		Namespace:   inst.Kubernetes.Namespace,
	})
	if err != nil {
		return err
	}
	selector := klabels.SelectorFromSet(inst.Kubernetes.Labels).String()
	ns := inst.Kubernetes.Namespace
	if ns == "" {
		ns = kc.namespace
	}

	deleteOpts := metav1.DeleteOptions{}
	listOpts := metav1.ListOptions{LabelSelector: selector}
	deleters := []func(context.Context) error{
		func(ctx context.Context) error {
			return kc.client.BatchV1().Jobs(ns).DeleteCollection(ctx, deleteOpts, listOpts)
		},
		func(ctx context.Context) error {
			return kc.client.AppsV1().Deployments(ns).DeleteCollection(ctx, deleteOpts, listOpts)
		},
		func(ctx context.Context) error {
			return kc.deleteServices(ctx, ns, deleteOpts, listOpts)
		},
		func(ctx context.Context) error {
			return kc.client.CoreV1().ConfigMaps(ns).DeleteCollection(ctx, deleteOpts, listOpts)
		},
		func(ctx context.Context) error {
			return kc.client.CoreV1().Secrets(ns).DeleteCollection(ctx, deleteOpts, listOpts)
		},
		func(ctx context.Context) error {
			return kc.client.CoreV1().ServiceAccounts(ns).DeleteCollection(ctx, deleteOpts, listOpts)
		},
		func(ctx context.Context) error {
			return kc.client.RbacV1().ClusterRoleBindings().DeleteCollection(ctx, deleteOpts, listOpts)
		},
	}
	for _, del := range deleters {
		if err := del(ctx); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	switch inst.Engine {
	case "ssh":
		_ = os.Remove(fabssh.KeyPath(inst.ArtifactKey()))
	case "kubernetes":
		_ = os.Remove(fabkube.KubeconfigPath(inst.ArtifactKey()))
	}
	return nil
}

func (k *kubeClient) deleteServices(ctx context.Context, namespace string, deleteOpts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	items, err := k.client.CoreV1().Services(namespace).List(ctx, listOpts)
	if err != nil {
		return err
	}
	for _, svc := range items.Items {
		if err := k.client.CoreV1().Services(namespace).Delete(ctx, svc.Name, deleteOpts); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func newClient(opts target.Options) (*kubeClient, error) {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.Kubeconfig != "" {
		loading.ExplicitPath = opts.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if opts.KubeContext != "" {
		overrides.CurrentContext = opts.KubeContext
	}
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides)
	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	raw, _ := cfg.RawConfig()
	contextName := opts.KubeContext
	if contextName == "" {
		contextName = raw.CurrentContext
	}
	ns := opts.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	return &kubeClient{
		client:         client,
		restConfig:     restCfg,
		contextName:    contextName,
		kubeconfigPath: opts.Kubeconfig,
		namespace:      ns,
	}, nil
}

func (k *kubeClient) ensureNamespace(ctx context.Context) error {
	_, err := k.client.CoreV1().Namespaces().Get(ctx, k.namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get namespace %s: %w", k.namespace, err)
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: k.namespace,
			Labels: map[string]string{
				managerLabel: "fab",
			},
		},
	}
	if _, err := k.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %s: %w", k.namespace, err)
	}
	return nil
}

func (k *kubeClient) createPostgres(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	base, labels := objectIdentity(name, p)
	db := engine.OrDefault(p.Defaults.Database, "fab")
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	seeds, err := sqlSeedData("postgres", p, true)
	if err != nil {
		return nil, err
	}
	if err := k.upsertSecret(ctx, base, labels, map[string][]byte{
		"POSTGRES_DB":       []byte(db),
		"POSTGRES_USER":     []byte(user),
		"POSTGRES_PASSWORD": []byte(pass),
	}); err != nil {
		return nil, err
	}
	if err := k.upsertConfigMap(ctx, base+"-seed", labels, seeds); err != nil {
		return nil, err
	}

	dep := dbDeployment(base, labels, image, 5432, []corev1.EnvVar{
		secretEnv("POSTGRES_DB", base, "POSTGRES_DB"),
		secretEnv("POSTGRES_USER", base, "POSTGRES_USER"),
		secretEnv("POSTGRES_PASSWORD", base, "POSTGRES_PASSWORD"),
	}, []corev1.Volume{configMapVolume("seed", base+"-seed")}, []corev1.VolumeMount{{Name: "seed", MountPath: "/docker-entrypoint-initdb.d", ReadOnly: true}}, nil, nil, nil)
	if err := k.upsertDeployment(ctx, dep); err != nil {
		return nil, err
	}
	if err := k.upsertService(ctx, service(base, labels, 5432)); err != nil {
		return nil, err
	}
	if err := k.waitDeployment(ctx, base, p, 120*time.Second); err != nil {
		return nil, err
	}

	host := serviceHost(base, k.namespace)
	return k.instance(name, p, image, base, labels, []engine.KubernetesObjectRef{
		ref("v1", "Secret", k.namespace, base),
		ref("v1", "ConfigMap", k.namespace, base+"-seed"),
		ref("apps/v1", "Deployment", k.namespace, base),
		ref("v1", "Service", k.namespace, base),
	}, engine.Creds{
		Engine:   p.Engine,
		Host:     host,
		Port:     5432,
		Username: user,
		Password: pass,
		Database: db,
		URL:      fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable", user, pass, host, 5432, db),
	}), nil
}

func (k *kubeClient) createMySQL(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	base, labels := objectIdentity(name, p)
	db := engine.OrDefault(p.Defaults.Database, "fab")
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	rootPass := engine.GeneratePassword()
	seeds, err := sqlSeedData("mysql", p, false)
	if err != nil {
		return nil, err
	}
	if err := k.upsertSecret(ctx, base, labels, map[string][]byte{
		"MYSQL_DATABASE":      []byte(db),
		"MYSQL_USER":          []byte(user),
		"MYSQL_PASSWORD":      []byte(pass),
		"MYSQL_ROOT_PASSWORD": []byte(rootPass),
	}); err != nil {
		return nil, err
	}
	if err := k.upsertConfigMap(ctx, base+"-seed", labels, seeds); err != nil {
		return nil, err
	}

	dep := dbDeployment(base, labels, image, 3306, []corev1.EnvVar{
		secretEnv("MYSQL_DATABASE", base, "MYSQL_DATABASE"),
		secretEnv("MYSQL_USER", base, "MYSQL_USER"),
		secretEnv("MYSQL_PASSWORD", base, "MYSQL_PASSWORD"),
		secretEnv("MYSQL_ROOT_PASSWORD", base, "MYSQL_ROOT_PASSWORD"),
	}, []corev1.Volume{configMapVolume("seed", base+"-seed")}, []corev1.VolumeMount{{Name: "seed", MountPath: "/docker-entrypoint-initdb.d", ReadOnly: true}}, nil, nil, nil)
	if err := k.upsertDeployment(ctx, dep); err != nil {
		return nil, err
	}
	if err := k.upsertService(ctx, service(base, labels, 3306)); err != nil {
		return nil, err
	}
	if err := k.waitDeployment(ctx, base, p, 180*time.Second); err != nil {
		return nil, err
	}

	host := serviceHost(base, k.namespace)
	return k.instance(name, p, image, base, labels, []engine.KubernetesObjectRef{
		ref("v1", "Secret", k.namespace, base),
		ref("v1", "ConfigMap", k.namespace, base+"-seed"),
		ref("apps/v1", "Deployment", k.namespace, base),
		ref("v1", "Service", k.namespace, base),
	}, engine.Creds{
		Engine:   p.Engine,
		Host:     host,
		Port:     3306,
		Username: user,
		Password: pass,
		Database: db,
		URL:      fmt.Sprintf("mysql://%s:%s@%s:%d/%s", user, pass, host, 3306, db),
	}), nil
}

func (k *kubeClient) createMongoDB(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	base, labels := objectIdentity(name, p)
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	db := engine.OrDefault(p.Defaults.Database, "fab")
	seeds, err := mongoSeedData(p)
	if err != nil {
		return nil, err
	}
	if err := k.upsertSecret(ctx, base, labels, map[string][]byte{
		"MONGO_INITDB_ROOT_USERNAME": []byte(user),
		"MONGO_INITDB_ROOT_PASSWORD": []byte(pass),
		"MONGO_INITDB_DATABASE":      []byte(db),
	}); err != nil {
		return nil, err
	}
	if err := k.upsertConfigMap(ctx, base+"-seed", labels, seeds); err != nil {
		return nil, err
	}

	dep := dbDeployment(base, labels, image, 27017, []corev1.EnvVar{
		secretEnv("MONGO_INITDB_ROOT_USERNAME", base, "MONGO_INITDB_ROOT_USERNAME"),
		secretEnv("MONGO_INITDB_ROOT_PASSWORD", base, "MONGO_INITDB_ROOT_PASSWORD"),
		secretEnv("MONGO_INITDB_DATABASE", base, "MONGO_INITDB_DATABASE"),
	}, []corev1.Volume{configMapVolume("seed", base+"-seed")}, []corev1.VolumeMount{{Name: "seed", MountPath: "/docker-entrypoint-initdb.d", ReadOnly: true}}, nil, nil, nil)
	if err := k.upsertDeployment(ctx, dep); err != nil {
		return nil, err
	}
	if err := k.upsertService(ctx, service(base, labels, 27017)); err != nil {
		return nil, err
	}
	if err := k.waitDeployment(ctx, base, p, 120*time.Second); err != nil {
		return nil, err
	}

	host := serviceHost(base, k.namespace)
	u := &url.URL{Scheme: "mongodb", User: url.UserPassword(user, pass), Host: fmt.Sprintf("%s:%d", host, 27017), Path: "/" + db}
	q := u.Query()
	q.Set("authSource", "admin")
	u.RawQuery = q.Encode()
	return k.instance(name, p, image, base, labels, []engine.KubernetesObjectRef{
		ref("v1", "Secret", k.namespace, base),
		ref("v1", "ConfigMap", k.namespace, base+"-seed"),
		ref("apps/v1", "Deployment", k.namespace, base),
		ref("v1", "Service", k.namespace, base),
	}, engine.Creds{
		Engine:   p.Engine,
		Host:     host,
		Port:     27017,
		Username: user,
		Password: pass,
		Database: db,
		URL:      u.String(),
	}), nil
}

func (k *kubeClient) createRedis(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	base, labels := objectIdentity(name, p)
	pass := p.Defaults.Password
	if pass == "" {
		pass = engine.GeneratePassword()
	}
	user := engine.OrDefault(p.Defaults.Username, "default")
	dbIdx := engine.OrDefault(p.Defaults.Database, "0")
	data, err := redisConfigAndSeeds(pass, p)
	if err != nil {
		return nil, err
	}
	if err := k.upsertSecret(ctx, base, labels, map[string][]byte{
		"REDIS_PASSWORD": []byte(pass),
		"REDIS_DB":       []byte(dbIdx),
	}); err != nil {
		return nil, err
	}
	if err := k.upsertConfigMap(ctx, base+"-config", labels, data); err != nil {
		return nil, err
	}

	dep := dbDeployment(base, labels, image, 6379, nil,
		[]corev1.Volume{configMapVolume("config", base+"-config")},
		[]corev1.VolumeMount{{Name: "config", MountPath: "/etc/redis", ReadOnly: true}},
		[]string{"redis-server", "/etc/redis/redis.conf"}, nil, nil)
	if err := k.upsertDeployment(ctx, dep); err != nil {
		return nil, err
	}
	if err := k.upsertService(ctx, service(base, labels, 6379)); err != nil {
		return nil, err
	}
	if err := k.waitDeployment(ctx, base, p, 60*time.Second); err != nil {
		return nil, err
	}
	if len(p.Seed) > 0 {
		job := redisSeedJob(k.namespace, base+"-seed", base, labels, image)
		if err := k.runJob(ctx, job, p, 60*time.Second); err != nil {
			return nil, err
		}
	}

	host := serviceHost(base, k.namespace)
	u := &url.URL{Scheme: "redis", User: url.UserPassword("", pass), Host: fmt.Sprintf("%s:%d", host, 6379), Path: "/" + dbIdx}
	return k.instance(name, p, image, base, labels, []engine.KubernetesObjectRef{
		ref("v1", "Secret", k.namespace, base),
		ref("v1", "ConfigMap", k.namespace, base+"-config"),
		ref("apps/v1", "Deployment", k.namespace, base),
		ref("v1", "Service", k.namespace, base),
		ref("batch/v1", "Job", k.namespace, base+"-seed"),
	}, engine.Creds{
		Engine:   p.Engine,
		Host:     host,
		Port:     6379,
		Username: user,
		Password: pass,
		Database: dbIdx,
		URL:      u.String(),
	}), nil
}

func (k *kubeClient) createPrometheus(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	base, labels := objectIdentity(name, p)
	data, err := prometheusData(p)
	if err != nil {
		return nil, err
	}
	if err := k.upsertConfigMap(ctx, base+"-config", labels, data); err != nil {
		return nil, err
	}
	dep := dbDeployment(base, labels, image, 9090, nil,
		[]corev1.Volume{configMapVolume("config", base+"-config")},
		[]corev1.VolumeMount{{Name: "config", MountPath: "/etc/prometheus/fab", ReadOnly: true}},
		nil, []string{
			"--config.file=/etc/prometheus/fab/prometheus.yml",
			"--storage.tsdb.path=/prometheus",
			"--web.console.libraries=/usr/share/prometheus/console_libraries",
			"--web.console.templates=/usr/share/prometheus/consoles",
			"--web.enable-lifecycle",
		}, nil)
	if err := k.upsertDeployment(ctx, dep); err != nil {
		return nil, err
	}
	if err := k.upsertService(ctx, service(base, labels, 9090)); err != nil {
		return nil, err
	}
	if err := k.waitDeployment(ctx, base, p, 60*time.Second); err != nil {
		return nil, err
	}
	host := serviceHost(base, k.namespace)
	return k.instance(name, p, image, base, labels, []engine.KubernetesObjectRef{
		ref("v1", "ConfigMap", k.namespace, base+"-config"),
		ref("apps/v1", "Deployment", k.namespace, base),
		ref("v1", "Service", k.namespace, base),
	}, engine.Creds{
		Engine: p.Engine,
		Host:   host,
		Port:   9090,
		URL:    fmt.Sprintf("http://%s:%d", host, 9090),
	}), nil
}

func (k *kubeClient) createSSH(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	base, labels := objectIdentity(name, p)
	user := engine.OrDefault(p.Defaults.Username, "fab")
	pub, priv, err := generateSSHKeypair()
	if err != nil {
		return nil, fmt.Errorf("ssh target: generate keypair: %w", err)
	}
	seedData, err := shellSeedData(p)
	if err != nil {
		return nil, err
	}
	if err := k.upsertConfigMap(ctx, base+"-seed", labels, seedData); err != nil {
		return nil, err
	}

	env := []corev1.EnvVar{
		{Name: "PUBLIC_KEY", Value: pub},
		{Name: "USER_NAME", Value: user},
		{Name: "PASSWORD_ACCESS", Value: "false"},
		{Name: "SUDO_ACCESS", Value: "true"},
		{Name: "PUID", Value: "1000"},
		{Name: "PGID", Value: "1000"},
	}
	for k, v := range p.Env {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}
	var lifecycle *corev1.Lifecycle
	if len(seedData) > 0 {
		lifecycle = &corev1.Lifecycle{PostStart: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{Command: []string{
			"/bin/sh", "-c", `for f in /fab-seed/*; do [ -f "$f" ] && /bin/sh "$f"; done`,
		}}}}
	}
	dep := dbDeployment(base, labels, image, 2222, env,
		[]corev1.Volume{configMapVolume("seed", base+"-seed")},
		[]corev1.VolumeMount{{Name: "seed", MountPath: "/fab-seed", ReadOnly: true}},
		nil, nil, lifecycle)
	if err := k.upsertDeployment(ctx, dep); err != nil {
		return nil, err
	}
	if err := k.upsertService(ctx, service(base, labels, 2222)); err != nil {
		return nil, err
	}
	if err := k.waitDeployment(ctx, base, p, 180*time.Second); err != nil {
		return nil, err
	}

	artifactID := artifactID(k.namespace, base)
	if err := writeFile(fabssh.KeyPath(artifactID), []byte(priv), 0o600); err != nil {
		return nil, fmt.Errorf("ssh target: persist key: %w", err)
	}
	host := serviceHost(base, k.namespace)
	u := &url.URL{Scheme: "ssh", User: url.User(user), Host: fmt.Sprintf("%s:%d", host, 2222)}
	return k.instanceWithArtifact(name, p, image, base, artifactID, labels, []engine.KubernetesObjectRef{
		ref("v1", "ConfigMap", k.namespace, base+"-seed"),
		ref("apps/v1", "Deployment", k.namespace, base),
		ref("v1", "Service", k.namespace, base),
	}, engine.Creds{
		Engine:     p.Engine,
		Host:       host,
		Port:       2222,
		Username:   user,
		URL:        u.String(),
		PrivateKey: priv,
	}), nil
}

func (k *kubeClient) createKubernetesResource(ctx context.Context, name string, p *profile.Profile, image string) (*engine.Instance, error) {
	if len(p.Seed) > 0 {
		return nil, fmt.Errorf("kubernetes target: kubernetes engine seed steps are not supported")
	}
	base, labels := objectIdentity(name, p)
	saName := base + "-access"
	if err := k.upsertServiceAccount(ctx, saName, labels); err != nil {
		return nil, err
	}
	if err := k.upsertClusterRoleBinding(ctx, saName+"-binding", saName, labels); err != nil {
		return nil, err
	}
	smoke := smokeDeployment(base+"-smoke", labels)
	if err := k.upsertDeployment(ctx, smoke); err != nil {
		return nil, err
	}
	if err := k.waitDeploymentName(ctx, smoke.Name, 60*time.Second); err != nil {
		return nil, err
	}

	exp := int64((24 * time.Hour).Seconds())
	tok, err := k.client.CoreV1().ServiceAccounts(k.namespace).CreateToken(ctx, saName, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{ExpirationSeconds: &exp},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create serviceaccount token for %s/%s: %w", k.namespace, saName, err)
	}
	kubeconfig, err := k.serviceAccountKubeconfig(tok.Status.Token, k.namespace)
	if err != nil {
		return nil, err
	}
	artifactID := artifactID(k.namespace, base)
	if err := writeFile(fabkube.KubeconfigPath(artifactID), kubeconfig, 0o600); err != nil {
		return nil, fmt.Errorf("persist kubeconfig: %w", err)
	}
	host, port := apiHostPort(k.restConfig.Host)
	return k.instanceWithArtifact(name, p, image, base, artifactID, labels, []engine.KubernetesObjectRef{
		{APIVersion: "v1", Kind: "ServiceAccount", Namespace: k.namespace, Name: saName},
		{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding", Name: saName + "-binding"},
		{APIVersion: "apps/v1", Kind: "Deployment", Namespace: k.namespace, Name: smoke.Name},
	}, engine.Creds{
		Engine:     p.Engine,
		Host:       host,
		Port:       port,
		URL:        k.restConfig.Host,
		Username:   saName,
		Kubeconfig: string(kubeconfig),
	}), nil
}

func (k *kubeClient) instance(name string, p *profile.Profile, image, base string, labels map[string]string, objects []engine.KubernetesObjectRef, creds engine.Creds) *engine.Instance {
	return k.instanceWithArtifact(name, p, image, base, artifactID(k.namespace, base), labels, objects, creds)
}

func (k *kubeClient) instanceWithArtifact(name string, p *profile.Profile, image, base, artifact string, labels map[string]string, objects []engine.KubernetesObjectRef, creds engine.Creds) *engine.Instance {
	return &engine.Instance{
		Name:       name,
		Profile:    p.Name,
		Engine:     p.Engine,
		Image:      image,
		Target:     target.Kubernetes,
		ArtifactID: artifact,
		Kubernetes: &engine.KubernetesState{
			Context:        k.contextName,
			KubeconfigPath: k.kubeconfigPath,
			Namespace:      k.namespace,
			EndpointMode:   target.EndpointCluster,
			Labels:         copyStringMap(labels),
			Objects:        objects,
		},
		Creds:     creds,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func (k *kubeClient) upsertSecret(ctx context.Context, name string, labels map[string]string, data map[string][]byte) error {
	existing, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().Secrets(k.namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.namespace, Labels: copyStringMap(labels)},
			Type:       corev1.SecretTypeOpaque,
			Data:       data,
		}, metav1.CreateOptions{})
		return wrapObjectErr("create", "secret", name, err)
	}
	if err != nil {
		return wrapObjectErr("get", "secret", name, err)
	}
	existing.Labels = copyStringMap(labels)
	existing.Type = corev1.SecretTypeOpaque
	existing.Data = data
	_, err = k.client.CoreV1().Secrets(k.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return wrapObjectErr("update", "secret", name, err)
}

func (k *kubeClient) upsertConfigMap(ctx context.Context, name string, labels map[string]string, data map[string]string) error {
	if data == nil {
		data = map[string]string{}
	}
	existing, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().ConfigMaps(k.namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.namespace, Labels: copyStringMap(labels)},
			Data:       data,
		}, metav1.CreateOptions{})
		return wrapObjectErr("create", "configmap", name, err)
	}
	if err != nil {
		return wrapObjectErr("get", "configmap", name, err)
	}
	existing.Labels = copyStringMap(labels)
	existing.Data = data
	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return wrapObjectErr("update", "configmap", name, err)
}

func (k *kubeClient) upsertDeployment(ctx context.Context, dep *appsv1.Deployment) error {
	dep.Namespace = k.namespace
	existing, err := k.client.AppsV1().Deployments(k.namespace).Get(ctx, dep.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.AppsV1().Deployments(k.namespace).Create(ctx, dep, metav1.CreateOptions{})
		return wrapObjectErr("create", "deployment", dep.Name, err)
	}
	if err != nil {
		return wrapObjectErr("get", "deployment", dep.Name, err)
	}
	dep.ResourceVersion = existing.ResourceVersion
	_, err = k.client.AppsV1().Deployments(k.namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err == nil {
		return nil
	}
	if apierrors.IsInvalid(err) {
		if delErr := k.client.AppsV1().Deployments(k.namespace).Delete(ctx, dep.Name, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
			return wrapObjectErr("delete", "deployment", dep.Name, delErr)
		}
		if waitErr := k.waitGone(ctx, "deployment", dep.Name); waitErr != nil {
			return waitErr
		}
		dep.ResourceVersion = ""
		_, err = k.client.AppsV1().Deployments(k.namespace).Create(ctx, dep, metav1.CreateOptions{})
	}
	return wrapObjectErr("update", "deployment", dep.Name, err)
}

func (k *kubeClient) upsertService(ctx context.Context, svc *corev1.Service) error {
	svc.Namespace = k.namespace
	existing, err := k.client.CoreV1().Services(k.namespace).Get(ctx, svc.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().Services(k.namespace).Create(ctx, svc, metav1.CreateOptions{})
		return wrapObjectErr("create", "service", svc.Name, err)
	}
	if err != nil {
		return wrapObjectErr("get", "service", svc.Name, err)
	}
	existing.Labels = copyStringMap(svc.Labels)
	existing.Spec.Selector = svc.Spec.Selector
	existing.Spec.Ports = svc.Spec.Ports
	_, err = k.client.CoreV1().Services(k.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return wrapObjectErr("update", "service", svc.Name, err)
}

func (k *kubeClient) upsertServiceAccount(ctx context.Context, name string, labels map[string]string) error {
	existing, err := k.client.CoreV1().ServiceAccounts(k.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.CoreV1().ServiceAccounts(k.namespace).Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.namespace, Labels: copyStringMap(labels)},
		}, metav1.CreateOptions{})
		return wrapObjectErr("create", "serviceaccount", name, err)
	}
	if err != nil {
		return wrapObjectErr("get", "serviceaccount", name, err)
	}
	existing.Labels = copyStringMap(labels)
	_, err = k.client.CoreV1().ServiceAccounts(k.namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return wrapObjectErr("update", "serviceaccount", name, err)
}

func (k *kubeClient) upsertClusterRoleBinding(ctx context.Context, name, saName string, labels map[string]string) error {
	want := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: copyStringMap(labels)},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      saName,
			Namespace: k.namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
	}
	existing, err := k.client.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = k.client.RbacV1().ClusterRoleBindings().Create(ctx, want, metav1.CreateOptions{})
		return wrapObjectErr("create", "clusterrolebinding", name, err)
	}
	if err != nil {
		return wrapObjectErr("get", "clusterrolebinding", name, err)
	}
	if existing.RoleRef != want.RoleRef {
		if err := k.client.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return wrapObjectErr("delete", "clusterrolebinding", name, err)
		}
		_, err = k.client.RbacV1().ClusterRoleBindings().Create(ctx, want, metav1.CreateOptions{})
		return wrapObjectErr("create", "clusterrolebinding", name, err)
	}
	existing.Labels = copyStringMap(labels)
	existing.Subjects = want.Subjects
	_, err = k.client.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
	return wrapObjectErr("update", "clusterrolebinding", name, err)
}

func (k *kubeClient) runJob(ctx context.Context, job *batchv1.Job, p *profile.Profile, fallback time.Duration) error {
	job.Namespace = k.namespace
	_ = k.client.BatchV1().Jobs(k.namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
	_ = k.waitGone(ctx, "job", job.Name)
	if _, err := k.client.BatchV1().Jobs(k.namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return wrapObjectErr("create", "job", job.Name, err)
	}
	timeout := engine.ParseTimeout(p.Healthcheck.Timeout, fallback)
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		got, err := k.client.BatchV1().Jobs(k.namespace).Get(ctx, job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if got.Status.Failed > 0 {
			return false, fmt.Errorf("job %s failed", job.Name)
		}
		return got.Status.Succeeded > 0, nil
	})
}

func (k *kubeClient) waitDeployment(ctx context.Context, name string, p *profile.Profile, fallback time.Duration) error {
	return k.waitDeploymentName(ctx, name, engine.ParseTimeout(p.Healthcheck.Timeout, fallback))
}

func (k *kubeClient) waitDeploymentName(ctx context.Context, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		got, err := k.client.AppsV1().Deployments(k.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, c := range got.Status.Conditions {
			if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return got.Status.ReadyReplicas >= 1, nil
	})
}

func (k *kubeClient) waitGone(ctx context.Context, kind, name string) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var err error
		switch kind {
		case "deployment":
			_, err = k.client.AppsV1().Deployments(k.namespace).Get(ctx, name, metav1.GetOptions{})
		case "job":
			_, err = k.client.BatchV1().Jobs(k.namespace).Get(ctx, name, metav1.GetOptions{})
		}
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

func (k *kubeClient) serviceAccountKubeconfig(token, namespace string) ([]byte, error) {
	ca, err := caData(k.restConfig)
	if err != nil {
		return nil, err
	}
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["fab-target"] = &clientcmdapi.Cluster{
		Server:                   k.restConfig.Host,
		CertificateAuthorityData: ca,
		InsecureSkipTLSVerify:    len(ca) == 0,
	}
	cfg.AuthInfos["fab-token"] = &clientcmdapi.AuthInfo{Token: token}
	cfg.Contexts["fab"] = &clientcmdapi.Context{Cluster: "fab-target", AuthInfo: "fab-token", Namespace: namespace}
	cfg.CurrentContext = "fab"
	return clientcmd.Write(*cfg)
}

func caData(cfg *rest.Config) ([]byte, error) {
	if len(cfg.CAData) > 0 {
		return cfg.CAData, nil
	}
	if cfg.CAFile != "" {
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read kube CA file %s: %w", cfg.CAFile, err)
		}
		return data, nil
	}
	return nil, nil
}

func dbDeployment(name string, labels map[string]string, image string, port int32, env []corev1.EnvVar, volumes []corev1.Volume, mounts []corev1.VolumeMount, command []string, args []string, lifecycle *corev1.Lifecycle) *appsv1.Deployment {
	replicas := int32(1)
	podLabels := copyStringMap(labels)
	podLabels[roleLabel] = "server"
	container := corev1.Container{
		Name:            "main",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Ports:           []corev1.ContainerPort{{Name: "main", ContainerPort: port}},
		Env:             env,
		VolumeMounts:    mounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)}},
			PeriodSeconds:    2,
			FailureThreshold: 30,
		},
		Lifecycle: lifecycle,
	}
	if len(command) > 0 {
		container.Command = command
	}
	if len(args) > 0 {
		container.Args = args
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: copyStringMap(labels)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{instanceLabel: labels[instanceLabel], roleLabel: "server"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
					Volumes:    volumes,
				},
			},
		},
	}
}

func smokeDeployment(name string, labels map[string]string) *appsv1.Deployment {
	dep := dbDeployment(name, labels, "busybox:1.36", 8080, nil, nil, nil, []string{"/bin/sh", "-c", `while true; do echo "fabricate smoke pod $(date -Iseconds)"; sleep 30; done`}, nil, nil)
	dep.Spec.Replicas = int32Ptr(2)
	dep.Spec.Template.Spec.Containers[0].ReadinessProbe = nil
	return dep
}

func service(name string, labels map[string]string, port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: copyStringMap(labels)},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{instanceLabel: labels[instanceLabel], roleLabel: "server"},
			Ports: []corev1.ServicePort{{
				Name:       "main",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
		},
	}
}

func redisSeedJob(namespace, name, serviceName string, labels map[string]string, image string) *batchv1.Job {
	backoff := int32(1)
	podLabels := copyStringMap(labels)
	podLabels[roleLabel] = "job"
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: copyStringMap(labels)},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Volumes:       []corev1.Volume{configMapVolume("config", serviceName+"-config")},
					Containers: []corev1.Container{{
						Name:            "seed",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command: []string{"/bin/sh", "-c", `set -eu
for f in /seed/*.redis; do
  [ -e "$f" ] || continue
  redis-cli -h "$REDIS_HOST" -a "$REDIS_PASSWORD" --no-auth-warning -n "$REDIS_DB" < "$f"
done`},
						Env: []corev1.EnvVar{
							{Name: "REDIS_HOST", Value: serviceHost(serviceName, namespace)},
							secretEnv("REDIS_PASSWORD", serviceName, "REDIS_PASSWORD"),
							secretEnv("REDIS_DB", serviceName, "REDIS_DB"),
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "config", MountPath: "/seed", ReadOnly: true}},
					}},
				},
			},
		},
	}
}

func configMapVolume(name, cm string) corev1.Volume {
	mode := int32(0o644)
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: cm},
			DefaultMode:          &mode,
		}},
	}
}

func secretEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secret},
			Key:                  key,
		}},
	}
}

func sqlSeedData(engineName string, p *profile.Profile, postgresExtensions bool) (map[string]string, error) {
	out := map[string]string{}
	if postgresExtensions && len(p.Extensions) > 0 {
		var b strings.Builder
		for _, ext := range p.Extensions {
			fmt.Fprintf(&b, "CREATE EXTENSION IF NOT EXISTS %q;\n", ext)
		}
		out["00-fab-extensions.sql"] = b.String()
	}
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeSQL {
			return nil, fmt.Errorf("%s kubernetes target: seed step %d type=%q not supported (sql only)", engineName, i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			return nil, fmt.Errorf("%s kubernetes target: read seed %q: %w", engineName, step.File, err)
		}
		out[seedKey(i, step.File, "sql")] = string(data)
	}
	return out, nil
}

func mongoSeedData(p *profile.Profile) (map[string]string, error) {
	out := map[string]string{}
	for i, step := range p.Seed {
		var ext string
		switch step.Type {
		case profile.SeedTypeJS:
			ext = "js"
		case profile.SeedTypeMongoImport, profile.SeedTypeShell:
			ext = "sh"
		default:
			return nil, fmt.Errorf("mongodb kubernetes target: seed step %d type=%q not supported (js, mongoimport, shell)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			return nil, fmt.Errorf("mongodb kubernetes target: read seed %q: %w", step.File, err)
		}
		out[seedKey(i, step.File, ext)] = string(data)
	}
	return out, nil
}

func redisConfigAndSeeds(pass string, p *profile.Profile) (map[string]string, error) {
	out := map[string]string{"redis.conf": fmt.Sprintf("requirepass %s\n", pass)}
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeRedisCLI {
			return nil, fmt.Errorf("redis kubernetes target: seed step %d type=%q not supported (redis-cli only)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			return nil, fmt.Errorf("redis kubernetes target: read seed %q: %w", step.File, err)
		}
		out[seedKey(i, step.File, "redis")] = string(data)
	}
	return out, nil
}

func prometheusData(p *profile.Profile) (map[string]string, error) {
	out := map[string]string{}
	var ruleNames []string
	for i, step := range p.Seed {
		data, err := p.ReadSeed(step)
		if err != nil {
			return nil, fmt.Errorf("prometheus kubernetes target: read seed %q: %w", step.File, err)
		}
		switch step.Type {
		case profile.SeedTypePromConfig:
			if _, ok := out["prometheus.yml"]; ok {
				return nil, fmt.Errorf("prometheus kubernetes target: seed step %d: only one prom-config allowed", i)
			}
			out["prometheus.yml"] = string(data)
		case profile.SeedTypePromRule:
			name := configKey(filepath.Base(step.File))
			out[name] = string(data)
			ruleNames = append(ruleNames, name)
		default:
			return nil, fmt.Errorf("prometheus kubernetes target: seed step %d type=%q not supported (prom-config, prom-rule)", i, step.Type)
		}
	}
	if _, ok := out["prometheus.yml"]; !ok {
		out["prometheus.yml"] = synthPromConfig(ruleNames)
	}
	return out, nil
}

func shellSeedData(p *profile.Profile) (map[string]string, error) {
	out := map[string]string{}
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeShell {
			return nil, fmt.Errorf("ssh kubernetes target: seed step %d type=%q not supported (shell only)", i, step.Type)
		}
		data, err := p.ReadSeed(step)
		if err != nil {
			return nil, fmt.Errorf("ssh kubernetes target: read seed %q: %w", step.File, err)
		}
		out[seedKey(i, step.File, "sh")] = string(data)
	}
	return out, nil
}

func synthPromConfig(ruleNames []string) string {
	var b strings.Builder
	b.WriteString("global:\n")
	b.WriteString("  scrape_interval: 15s\n")
	b.WriteString("  evaluation_interval: 15s\n")
	b.WriteString("\nscrape_configs:\n")
	b.WriteString("  - job_name: prometheus\n")
	b.WriteString("    static_configs:\n")
	b.WriteString("      - targets: [localhost:9090]\n")
	if len(ruleNames) > 0 {
		b.WriteString("\nrule_files:\n")
		for _, r := range ruleNames {
			fmt.Fprintf(&b, "  - /etc/prometheus/fab/%s\n", r)
		}
	}
	return b.String()
}

func generateSSHKeypair() (publicAuthorizedLine, privatePEM string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	sshPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("wrap ed25519 pub: %w", err)
	}
	block, err := xssh.MarshalPrivateKey(priv, "fab")
	if err != nil {
		return "", "", fmt.Errorf("marshal ed25519 priv: %w", err)
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, block); err != nil {
		return "", "", err
	}
	return string(xssh.MarshalAuthorizedKey(sshPub)), buf.String(), nil
}

func objectIdentity(name string, p *profile.Profile) (string, map[string]string) {
	base := dnsLabel(name, 48)
	return base, map[string]string{
		managerLabel:  "fab",
		instanceLabel: dnsLabel(name, 63),
		engineLabel:   dnsLabel(p.Engine, 63),
		profileLabel:  dnsLabel(p.Name, 63),
	}
}

func ref(apiVersion, kind, namespace, name string) engine.KubernetesObjectRef {
	return engine.KubernetesObjectRef{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name}
}

func serviceHost(name, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
}

func artifactID(namespace, name string) string {
	return "k8s-" + dnsLabel(namespace, 24) + "-" + dnsLabel(name, 40)
}

func seedKey(i int, file, ext string) string {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	return fmt.Sprintf("%02d-%s.%s", i+10, configKey(base), ext)
}

func configKey(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "seed"
	}
	return out
}

func dnsLabel(s string, max int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "fab"
	}
	if max > 0 && len(out) > max {
		out = strings.Trim(out[:max], "-")
	}
	if out == "" {
		out = "fab"
	}
	return out
}

func apiHostPort(raw string) (string, int) {
	u, err := url.Parse(raw)
	if err != nil {
		return raw, 443
	}
	host := u.Hostname()
	port := 443
	if u.Port() != "" {
		if _, err := fmt.Sscanf(u.Port(), "%d", &port); err != nil {
			port = 443
		}
	}
	if host == "" {
		host = raw
	}
	return host, port
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func int32Ptr(v int32) *int32 { return &v }

func wrapObjectErr(verb, kind, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s %s: %w", verb, kind, name, err)
}
