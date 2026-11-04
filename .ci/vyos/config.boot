// Minimal disposable acceptance VM configuration for
// VyOS 2026.10.22-0026-rolling. Set interface addresses and PKI separately.
system {
    host-name provider-test
    login {
        user vyos {
            authentication {
                plaintext-password "lab-only"
            }
        }
    }
}
interfaces {
    loopback lo {
    }
}
service {
    https {
        api {
            keys {
                id provider-test {
                    key "replace-with-test-only-key"
                }
            }
            rest {
            }
        }
        port "443"
    }
}
