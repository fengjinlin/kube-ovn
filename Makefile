REGISTRY?=registry.cn-hangzhou.aliyuncs.com/feng-k8s
ARCH?=amd64
BASE_TAG=v0.0.1
RELEASE_TAG=v0.0.1-alpha.1


.PHONY: build-image-base
build-image-base:
	docker buildx build --platform linux/amd64 --build-arg ARCH=amd64 -t kube-ovn-base:$(BASE_TAG) -o type=docker -f dist/images/Dockerfile.base dist/images/

.PHONY: build-image-ovn
build-image-ovn: lint build-go
	# --no-cache --progress=plain
	docker buildx build --platform linux/amd64  -t $(REGISTRY)/kube-ovn:$(RELEASE_TAG) --build-arg VERSION=$(BASE_TAG) -o type=docker -f dist/images/Dockerfile dist/images/

.PHONY: code-gen
code-gen:
	bash hack/update-codegen.sh

.PHONY: model-gen
model-gen:
	if ! which modelgen >/dev/null 2>&1; then \
		go install github.com/ovn-org/libovsdb/cmd/modelgen@v0.0.0-20230711201130-6785b52d4020; \
	fi
	go generate github.com/fengjinlin/kube-ovn/pkg/ovsdb/schema/...

.PHONY: crd-gen
crd-gen:
	if ! which controller-gen >/dev/null 2>&1; then \
    	go install sigs.k8s.io/controller-tools/cmd/{controller-gen,type-scaffold}@v0.15.0; \
    fi
	controller-gen crd:allowDangerousTypes=true paths=./pkg/apis/... output:crd:dir=dist/install/crd
	# api-approved.kubernetes.io: "https://github.com/kubernetes/kubernetes/pull/78458"

.PHONY: build-go
build-go:
	go mod tidy
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -o $(CURDIR)/dist/images/kube-ovn -v ./cmd/cni
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -buildmode=pie -o $(CURDIR)/dist/images/kube-ovn-cmd -v ./cmd

.PHONY: lint
lint:
	if ! which gosec >/dev/null 2>&1; then \
		go install github.com/securego/gosec/v2/cmd/gosec@latest; \
	fi
	@gofmt -d .
	@if [ $$(gofmt -l . | wc -l) -ne 0 ]; then \
		echo "Code differs from gofmt's style" 1>&2 && exit 1; \
	fi
	@GOOS=linux go vet ./...
	@GOOS=linux gosec -exclude=G204,G301,G306,G402,G404,G601 -exclude-dir=test -exclude-dir=pkg/client ./...

.PHONY: tar
tar:
	tar -zcvf kube-ovn-$(RELEASE_TAG).tgz -C dist/install/ --exclude=tpl .

.PHONY: release
release:
	# crd
	$(MAKE) crd-gen
	@for file in `ls dist/install/crd/*`; do \
		sed -i '6a \    api-approved.kubernetes.io: "https://github.com/kubernetes/kubernetes/pull/78458"' $$file;  \
	done
	# image
	$(MAKE) build-image-ovn
	docker push $(REGISTRY)/kube-ovn:$(RELEASE_TAG)
	# yaml
	cp dist/install/tpl/*.tpl dist/install/
	@for file in `ls dist/install/*.tpl`; do \
    	new_file=$${file%.*} ; \
    	mv $$file $$new_file ; \
    	sed -i 's#{REGISTRY}#$(REGISTRY)#g' $$new_file ; \
    	sed -i 's#{RELEASE_TAG}#$(RELEASE_TAG)#g' $$new_file ; \
    done
    # tar
	$(MAKE) tar