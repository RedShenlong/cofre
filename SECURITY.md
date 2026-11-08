# Política de seguridad

Cofre protege datos personales, así que cualquier fallo de seguridad me interesa mucho.

## Cómo informar de una vulnerabilidad

**No abras un issue público.** Usa el apartado
[Security → Report a vulnerability](https://github.com/RedShenlong/cofre/security/advisories/new)
de este repositorio, que es privado.

Incluye, si puedes:
- qué componente está afectado (WebAuthn, TLS, cifrado en disco, API, CLI…),
- los pasos para reproducirlo,
- el impacto que crees que tiene.

Intentaré responder en unos pocos días y publicar la corrección lo antes posible.

## Alcance

Cofre está pensado para usarse **dentro de una red local de confianza**. No está diseñado
para exponerse directamente a internet. Si lo haces, es bajo tu responsabilidad.

El modelo de amenazas completo está en la sección
[Seguridad del README](README.md#seguridad).
