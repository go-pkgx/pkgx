# The claim this image exists to test: zpkgx and zpkgs answer about the whole
# dependency tree with NOTHING else on the filesystem and NO network.
#
# FROM scratch is the test, not a deployment style. There is no shell, no
# libc, no CA bundle, no /tmp and no /etc/resolv.conf: anything the binaries
# reach for that is not here fails loudly instead of being quietly supplied
# by a base image, which is what a base image is for and why it hides this.
#
# No RUN step. `zpkgs catalog update` writes the .names index beside the
# catalogue, so the image carries both and executes nothing at build time —
# every byte in it was put there by a COPY somebody can read.
FROM scratch

COPY zpkgx /zpkgx
COPY zpkgs /zpkgs
COPY catalog/ /store/catalog/

# The store a real user has. The catalogue is read from
# $ZPKG_DIR/catalog/<os>-<arch>.json, which is the path tab-completion's name
# index is keyed on. ZPKG_CATALOG names a file instead and deliberately
# bypasses that index (internal/app/nameindex.go), so an image built around
# it would measure the slow path and report it as the fast one.
ENV ZPKG_DIR=/store

ENTRYPOINT ["/zpkgs"]
