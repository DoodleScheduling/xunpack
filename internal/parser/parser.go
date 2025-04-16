package parser

import (
	"archive/tar"
	"bufio"
	"context"
	"io"
	"os"
	"strings"

	crossplaneapiextv1 "github.com/crossplane/crossplane/apis/apiextensions/v1"
	crossplanev1 "github.com/crossplane/crossplane/apis/pkg/v1"
	"github.com/doodlescheduling/xunpack/internal/worker"
	"github.com/doodlescheduling/xunpack/internal/xcrd"
	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/cli-runtime/pkg/printers"
)

type Parser struct {
	Output       io.Writer
	AllowFailure bool
	FailFast     bool
	Workers      int
	Decoder      runtime.Decoder
	Logger       logr.Logger
	Printer      printers.ResourcePrinter
}

func (p *Parser) Run(ctx context.Context, in io.Reader) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	abort := func(err error) error {
		if err == nil {
			return nil
		}

		if p.FailFast {
			cancel()
		}

		return err
	}

	pool := worker.New(ctx, worker.PoolOptions{
		Workers: p.Workers,
	})

	outWriter := worker.New(ctx, worker.PoolOptions{
		Workers: 1,
	})

	objects := make(chan runtime.Object, p.Workers)

	outWriter.Push(worker.Task(func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case obj, ok := <-objects:
				if !ok {
					return nil
				}

				err := p.Printer.PrintObj(obj, p.Output)

				if err != nil {
					p.Logger.Error(err, "failed to write manifests to output")
					return abort(err)
				}
			}
		}
	}))

	multidocReader := utilyaml.NewYAMLReader(bufio.NewReader(in))

	for {
		resourceYAML, err := multidocReader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}

			return err
		}

		pool.Push(worker.Task(func(ctx context.Context) error {
			obj, gvk, err := p.Decoder.Decode(
				resourceYAML,
				nil,
				nil)
			if err != nil {
				return nil
			}

			return p.handleResource(obj, gvk, objects)
		}))
	}

	p.exit(pool)
	close(objects)
	p.exit(outWriter)
	return nil
}

func (p *Parser) exit(waiters ...worker.Waiter) {
	for _, w := range waiters {
		err := w.Wait()
		if err != nil && !p.AllowFailure {
			p.Logger.Error(err, "error occured")
			os.Exit(1)
		}
	}
}

func (p *Parser) handleResource(obj runtime.Object, gvk *schema.GroupVersionKind, out chan runtime.Object) error {
	if gvk.Group == "pkg.crossplane.io" && gvk.Kind == "Provider" {
		provider := obj.(*crossplanev1.Provider)
		p.Logger.Info("unpacking provider", "name", provider.Name, "url", provider.Spec.Package)

		manifest, err := p.unpack(provider)

		if err != nil {
			return err
		}

		return p.parseManifest(manifest, out)
	} else if gvk.Version == "v1" && gvk.Group == "apiextensions.crossplane.io" && gvk.Kind == "CompositeResourceDefinition" {
		xcrDefinition := obj.(*crossplaneapiextv1.CompositeResourceDefinition)
		crd, err := xcrd.ForCompositeResource(xcrDefinition)
		if err != nil {
			return err
		}

		crd.Kind = "CustomResourceDefinition"
		crd.APIVersion = "apiextensions.k8s.io/v1"
		crd.OwnerReferences = nil
		out <- crd

		if xcrDefinition.Spec.ClaimNames.Kind != "" {
			crd, err := xcrd.ForCompositeResourceClaim(xcrDefinition)
			if err != nil {
				return err
			}

			crd.Kind = "CustomResourceDefinition"
			crd.APIVersion = "apiextensions.k8s.io/v1"
			crd.OwnerReferences = nil
			out <- crd
		}
	}

	return nil
}

func (p *Parser) parseManifest(manifest []byte, out chan runtime.Object) error {
	for _, resourceYAML := range strings.Split(string(manifest), "---") {
		if len(resourceYAML) == 0 {
			continue
		}

		obj := unstructured.Unstructured{}
		_, gvk, err := p.Decoder.Decode(
			[]byte(resourceYAML),
			nil,
			&obj)

		if err != nil && !runtime.IsMissingKind(err) {
			return err
		} else if runtime.IsMissingKind(err) {
			continue
		}

		// exclude meta resources
		if gvk.Group != "meta.pkg.crossplane.io" {
			out <- &obj
		}
	}

	return nil
}

func (p *Parser) unpack(pkg *crossplanev1.Provider) ([]byte, error) {
	ref, err := name.ParseReference(pkg.Spec.Package)
	if err != nil {
		return nil, err
	}

	img, err := remote.Image(ref)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "xpk")
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = os.Remove(tmpDir)
	}()

	tb, err := os.CreateTemp("", "image")
	if err != nil {
		return nil, err
	}

	err = tarball.Write(ref, img, tb)
	if err != nil {
		return nil, err
	}

	ociImage, err := tarball.ImageFromPath(tb.Name(), nil)
	if err != nil {
		return nil, err
	}

	layers, err := ociImage.Layers()
	if err != nil {
		return nil, err
	}

	for _, layer := range layers {
		contents, err := layer.Uncompressed()
		if err != nil {
			return nil, err
		}

		compressedLayer, err := os.CreateTemp("", "layer")
		if err != nil {
			return nil, err
		}

		defer func() {
			_ = compressedLayer.Close()
		}()

		_, err = io.Copy(compressedLayer, contents)
		if err != nil {
			return nil, err
		}

		_, err = compressedLayer.Seek(0, 0)
		if err != nil {
			return nil, err
		}

		manifest, err := p.extractPackageManifest(compressedLayer)

		if err != nil && err != io.EOF {
			return nil, err
		}

		if len(manifest) > 0 {
			return manifest, nil
		}
	}

	return nil, io.EOF

}

func (p *Parser) extractPackageManifest(r io.Reader) ([]byte, error) {
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()

		switch {
		case err == io.EOF:
			return nil, io.EOF

		case err != nil:
			return nil, err

		case header == nil:
			continue
		}

		if header.Name == "package.yaml" {
			return io.ReadAll(tr)
		}
	}
}
