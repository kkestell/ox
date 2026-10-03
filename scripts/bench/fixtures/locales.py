# Generates and checks the locales benchmark task: translation files in
# several formatting styles, each gaining the same new keys.
#
#   python3 locales.py setup|check

import json
import subprocess
import sys
from pathlib import Path

KEYS = ["title", "subtotal", "shipping", "tax", "total", "place_order", "back"]
NEW_KEYS = ["discount", "gift_card", "apply"]
# Each locale's strings for KEYS then NEW_KEYS, its indentation, and whether it
# escapes non-ASCII characters.
LOCALES = {
    "de": ("Kasse|Zwischensumme|Versand|MwSt.|Gesamtsumme|Bestellung aufgeben|Zurück zum Warenkorb", "Rabatt|Geschenkkarte|Anwenden", 2, False),
    "fr": ("Paiement|Sous-total|Livraison|TVA|Total|Passer la commande|Retour au panier", "Remise|Carte cadeau|Appliquer", 2, True),
    "es": ("Pago|Subtotal|Envío|IVA|Total|Realizar pedido|Volver al carrito", "Descuento|Tarjeta regalo|Aplicar", 4, False),
    "it": ("Cassa|Subtotale|Spedizione|IVA|Totale|Effettua l'ordine|Torna al carrello", "Sconto|Carta regalo|Applica", 2, False),
    "pt": ("Finalizar compra|Subtotal|Envio|Imposto|Total|Fazer pedido|Voltar ao carrinho", "Desconto|Cartão-presente|Aplicar", 2, True),
    "nl": ("Afrekenen|Subtotaal|Verzending|Btw|Totaal|Bestelling plaatsen|Terug naar winkelwagen", "Korting|Cadeaukaart|Toepassen", 4, False),
    "sv": ("Kassa|Delsumma|Frakt|Moms|Totalt|Lägg beställning|Tillbaka till varukorgen", "Rabatt|Presentkort|Använd", 2, False),
    "pl": ("Kasa|Suma częściowa|Wysyłka|VAT|Razem|Złóż zamówienie|Wróć do koszyka", "Rabat|Karta podarunkowa|Zastosuj", 2, True),
    "tr": ("Ödeme|Ara toplam|Kargo|KDV|Toplam|Siparişi ver|Sepete dön", "İndirim|Hediye kartı|Uygula", 4, False),
    "ru": ("Оформление заказа|Промежуточный итог|Доставка|НДС|Итого|Оформить заказ|Вернуться в корзину", "Скидка|Подарочная карта|Применить", 2, False),
    "uk": ("Оформлення|Проміжний підсумок|Доставка|ПДВ|Разом|Оформити замовлення|Назад до кошика", "Знижка|Подарункова картка|Застосувати", 2, True),
    "el": ("Ταμείο|Μερικό σύνολο|Αποστολή|ΦΠΑ|Σύνολο|Υποβολή παραγγελίας|Πίσω στο καλάθι", "Έκπτωση|Δωροκάρτα|Εφαρμογή", 2, False),
    "ja": ("レジ|小計|配送料|税|合計|注文を確定する|カートに戻る", "割引|ギフトカード|適用", 2, False),
    "ko": ("결제|소계|배송비|세금|합계|주문하기|장바구니로 돌아가기", "할인|기프트 카드|적용", 4, True),
    "zh": ("结账|小计|运费|税费|总计|提交订单|返回购物车", "折扣|礼品卡|使用", 2, False),
    "he": ("קופה|סכום ביניים|משלוח|מע\"מ|סה\"כ|ביצוע הזמנה|חזרה לעגלה", "הנחה|כרטיס מתנה|החל", 2, False),
}
OTHER = {
    "cart": {"empty": "∅", "items": "{count} ×"},
    "account": {"sign_in": "→", "sign_out": "←"},
}


def document(locale, with_new):
    strings, new, _, _ = LOCALES[locale]
    checkout = dict(zip(KEYS, strings.split("|")))
    if with_new:
        items = list(checkout.items())
        at = KEYS.index("total") + 1
        items[at:at] = zip(NEW_KEYS, new.split("|"))
        checkout = dict(items)
    return {"locale": locale, "cart": OTHER["cart"], "checkout": checkout, "account": OTHER["account"]}


def render(locale, with_new):
    _, _, indent, escape = LOCALES[locale]
    return json.dumps(document(locale, with_new), indent=indent, ensure_ascii=escape) + "\n"


def setup():
    directory = Path("locales")
    directory.mkdir()
    for locale in LOCALES:
        (directory / f"{locale}.json").write_text(render(locale, False))
    rows = ["| Locale | discount | gift_card | apply |", "| --- | --- | --- | --- |"]
    for locale, (_, new, _, _) in LOCALES.items():
        rows.append(f"| {locale} | " + " | ".join(new.split("|")) + " |")
    Path("TRANSLATIONS.md").write_text("# New checkout strings\n\n" + "\n".join(rows) + "\n")
    for args in (["init", "-q"], ["add", "-A"]):
        subprocess.run(["git", *args], check=True)
    subprocess.run(
        ["git", "-c", "user.name=bench", "-c", "user.email=bench@example.com", "commit", "-qm", "Initial"],
        check=True,
    )


def check():
    wrong = [
        locale
        for locale in LOCALES
        if Path(f"locales/{locale}.json").read_text() != render(locale, True)
    ]
    if wrong:
        print(f"FAIL: these files differ from the expected files: {', '.join(wrong)}")
        raise SystemExit(1)
    print("PASS")


if __name__ == "__main__":
    {"setup": setup, "check": check}[sys.argv[1]]()
