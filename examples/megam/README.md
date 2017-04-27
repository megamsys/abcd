# Kubeshift

This Guide provides the templates for deploying in ABCD (Onboard cloud).

## WARNING

These templates are used internally in [ABCD](https://gitlab.com/megamsys/abcd) and can be used publically from the web

[OnboardCloud Public]()

## Client Requirements

This is for the development team.

In addition to oc, Sigil is required for template handling and must be installed in your system PATH. Instructions can be found here: <https://github.com/gliderlabs/sigil>

## Cluster Requirements

At a High level:

- The Kubernetes SkyDNS addon needs to be set as a resolver on masters and nodes
- Ceph and RBD utilities must be installed on masters and nodes
- Linux Kernel should be newer than 4.2.0
- Docker 1.17 or newer



### OpenNebula

The Kubernetes kubelet shells out to system utilities to setup FrontEnd and Nodes.


### Ceph

[Refer README.md](ceph/README.md)


## Quickstart

If you're feeling confident:

```

```

This will most likely not work on your setup, see the rest of the guide if you encounter errors.

We will be working on making this setup more agnostic, especially in regards to the network IP ranges.
