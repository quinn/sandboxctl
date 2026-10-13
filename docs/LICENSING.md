# Project licensing

## Project code

Original sandboxctl code is licensed under the GNU Affero General Public
License, version 3 or any later version (AGPL-3.0-or-later). The verbatim
license text is in [`../LICENSE`](../LICENSE). The text was downloaded from
[GNU's official AGPL-3.0 text](https://www.gnu.org/licenses/agpl-3.0.txt).

The AGPL applies to project-authored code unless a file or directory has a
separate license notice. Do not remove third-party copyright or license
notices. Do not describe third-party code as solely AGPL-licensed.

## Third-party code

The project includes or builds with these third-party components:

| Component | License | How it is used |
| --- | --- | --- |
| E2B distribution profiles and provisioning code | Apache-2.0 | Selected source was adapted into `cmd/sandboxctl/image/build/distro` and `cmd/sandboxctl/image/build/provision`. The original copyright and license notices remain. |
| Firecracker and jailer | Apache-2.0 | Official binaries are included in the Arch package. |
| BuildKit v0.34.0 | Apache-2.0 | The pinned tools are included in the packaged builder image. |
| `golang.org/x/crypto/ssh` v0.58.0 | BSD-3-Clause | The CLI and daemon use native Go SSH for guest forwarding. The exact module license remains in `third_party/licenses/go/`. |
| Go modules used by the project binaries | Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, MIT, and MPL-2.0 | The exact license files from the modules in `go list -deps ./...` are in [`../third_party/licenses/go/`](../third_party/licenses/go/). Filenames include the module path and version. |
| Ubuntu packages in the builder image | Per-package licenses | Package copyright and license files remain in the Ubuntu guest filesystem. |

The package metadata lists the licenses used by the project and its included
components. The Arch packages install the project license and all files under
`third_party/licenses/` in the package-specific directory under
`/usr/share/licenses/`.

The dependency review found Apache-2.0, BSD, ISC, MIT, and MPL-2.0 terms. It
found no GPL-licensed Go module in the compiled dependency graph. GNU states
that Apache-2.0 is compatible with GNU AGPLv3. MPL-2.0 permits distributing
covered code under AGPLv3 as a secondary license unless the source carries an
incompatibility notice. The pinned HashiCorp MPL-2.0 modules do not carry that
notice. See [GNU license compatibility](https://www.gnu.org/licenses/license-compatibility.en.html)
and the [Mozilla Public License 2.0](https://www.mozilla.org/en-US/MPL/2.0/).

This inventory covers the current source and pinned dependencies. Review it
again when dependencies, embedded tools, or package contents change. It is a
project inventory, not legal advice.
