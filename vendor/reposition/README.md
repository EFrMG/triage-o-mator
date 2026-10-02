# Vendored Reposition build

The repository's root `LICENSE` covers triage-o-mator's own code. The archives in `0.2.0.dev1/` retain their separate upstream license terms and notices:

| Archive                                  | License material retained inside it                                             |
| ---------------------------------------- | ------------------------------------------------------------------------------- |
| `reposition-source.tar.gz`               | `reposition-source/LICENSE`, `NOTICE`, and `licenses/`                          |
| `reposition-0.2.0.dev1-py3-none-any.whl` | `reposition-0.2.0.dev1.dist-info/licenses/`                                     |
| `pip-25.3-py3-none-any.whl`              | `pip-25.3.dist-info/licenses/` and `pip/_vendor/` license files                 |
| `setuptools-84.0.0-py3-none-any.whl`     | `setuptools-84.0.0.dist-info/licenses/` and `setuptools/_vendor/` license files |

Reposition's package metadata declares `MIT AND Apache-2.0`; its `NOTICE` attributes the Apache-licensed adapted retrieval code and the MIT-licensed evidence validator. The pip and setuptools wheels also include licenses for code they vendor. The exact file hashes and source commit are recorded in `0.2.0.dev1/manifest.json`.
