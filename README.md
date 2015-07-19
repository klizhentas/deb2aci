# deb2aci

Deb2aci works like aptitude, but instead it downloads the deb packages with all their dependencies
and installs them into the ACI image.

It remembers each dependency in annotation to the resulting image manifest.

```
# outputs nginx.aci
deb2aci nginx nginx.manifest
```

The image manifest will keep track of what packages are part of it:

```
[
   {
      "name":"debian.org/deb/lsb-base",
      "value":"all/4.1+Debian11ubuntu8"
   },
   {
      "name":"debian.org/deb/libjbig0",
      "value":"amd64/2.1-3.1"
   },
   ...
]
```

