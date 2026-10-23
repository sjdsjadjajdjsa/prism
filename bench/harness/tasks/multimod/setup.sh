cat > pricing.py <<'EOF'
"""Money helpers. All amounts are Decimal dollars."""
from decimal import Decimal, ROUND_HALF_EVEN

CENT = Decimal("0.01")


def money(value):
    """Round to whole cents; exact halves round up (2.665 -> 2.67)."""
    return Decimal(str(value)).quantize(CENT, rounding=ROUND_HALF_EVEN)


def line_total(unit_price, qty, discount_pct=0):
    """Price of one order line after a percentage discount (0-100)."""
    gross = Decimal(str(unit_price)) * qty
    return money(gross * (100 - Decimal(str(discount_pct))) / 100)
EOF
cat > inventory.py <<'EOF'
"""Stock keeping."""


class OutOfStock(Exception):
    pass


class Inventory:
    def __init__(self, stock):
        self._stock = dict(stock)

    def available(self, sku):
        return self._stock.get(sku, 0)

    def reserve(self, lines):
        """Reserve [(sku, qty), ...]. All-or-nothing: if any line cannot be
        satisfied raise OutOfStock(sku) and leave the stock untouched.
        The same sku may appear on several lines."""
        for sku, qty in lines:
            if self._stock.get(sku, 0) < qty:
                raise OutOfStock(sku)
            self._stock[sku] -= qty

    def release(self, lines):
        for sku, qty in lines:
            self._stock[sku] = self._stock.get(sku, 0) + qty
EOF
cat > orders.py <<'EOF'
"""Order pricing and placement."""
from decimal import Decimal

from pricing import line_total, money

SHIPPING = Decimal("5.00")
FREE_SHIPPING_FROM = Decimal("50.00")   # net amount at or above this ships free
TAX_RATE = Decimal("0.08")


def summary(lines, catalog, coupon_pct=0):
    """lines: [(sku, qty)], catalog: {sku: unit price}.

    subtotal = sum of line totals
    discount = coupon_pct % of the subtotal, rounded to cents
    net      = subtotal - discount
    shipping = 0.00 when net >= FREE_SHIPPING_FROM else SHIPPING
    tax      = TAX_RATE * net, rounded to cents (shipping is not taxed)
    total    = net + shipping + tax
    """
    subtotal = sum((line_total(catalog[sku], qty) for sku, qty in lines), Decimal("0"))
    discount = money(subtotal * Decimal(str(coupon_pct)) / 100)
    net = subtotal - discount
    shipping = Decimal("0.00") if subtotal >= FREE_SHIPPING_FROM else SHIPPING
    tax = money(net * TAX_RATE)
    return {
        "subtotal": subtotal,
        "discount": discount,
        "net": net,
        "shipping": shipping,
        "tax": tax,
        "total": net + shipping + tax,
    }


def place_order(inventory, lines, catalog, coupon_pct=0):
    """Reserve stock, then price the order. Returns the summary."""
    inventory.reserve(lines)
    return summary(lines, catalog, coupon_pct)
EOF
cat > test_shop.py <<'EOF'
import unittest
from decimal import Decimal as D

from inventory import Inventory, OutOfStock
from orders import place_order, summary
from pricing import line_total, money

CATALOG = {"pen": D("1.25"), "book": D("12.40"), "lamp": D("33.33"), "cable": D("2.665")}


class PricingTests(unittest.TestCase):
    def test_money_rounds_halves_up(self):
        self.assertEqual(money("2.665"), D("2.67"))
        self.assertEqual(money("0.125"), D("0.13"))
        self.assertEqual(money("2.664"), D("2.66"))

    def test_line_total_discount(self):
        self.assertEqual(line_total("19.99", 3, 15), D("50.97"))
        self.assertEqual(line_total("1.25", 1, 50), D("0.63"))

    def test_line_total_no_discount(self):
        self.assertEqual(line_total("12.40", 2), D("24.80"))


class InventoryTests(unittest.TestCase):
    def test_reserve_reduces_stock(self):
        inv = Inventory({"pen": 10})
        inv.reserve([("pen", 3)])
        self.assertEqual(inv.available("pen"), 7)

    def test_failed_reserve_leaves_stock_untouched(self):
        inv = Inventory({"pen": 10, "book": 1})
        with self.assertRaises(OutOfStock) as cm:
            inv.reserve([("pen", 4), ("book", 2)])
        self.assertEqual(cm.exception.args[0], "book")
        self.assertEqual(inv.available("pen"), 10)
        self.assertEqual(inv.available("book"), 1)

    def test_duplicate_lines_are_summed(self):
        inv = Inventory({"pen": 5})
        with self.assertRaises(OutOfStock):
            inv.reserve([("pen", 3), ("pen", 3)])
        self.assertEqual(inv.available("pen"), 5)
        inv.reserve([("pen", 3), ("pen", 2)])
        self.assertEqual(inv.available("pen"), 0)

    def test_unknown_sku_is_out_of_stock(self):
        with self.assertRaises(OutOfStock):
            Inventory({}).reserve([("ghost", 1)])


class OrderTests(unittest.TestCase):
    def test_small_order_pays_shipping(self):
        s = summary([("pen", 4), ("book", 1)], CATALOG)
        self.assertEqual(s["subtotal"], D("17.40"))
        self.assertEqual(s["shipping"], D("5.00"))
        self.assertEqual(s["tax"], D("1.39"))
        self.assertEqual(s["total"], D("23.79"))

    def test_free_shipping_boundary_is_inclusive(self):
        s = summary([("pen", 40)], CATALOG)           # exactly 50.00
        self.assertEqual(s["shipping"], D("0.00"))

    def test_coupon_can_remove_free_shipping(self):
        # subtotal 66.66 qualifies for free shipping, but the net after a 25% coupon (66.66 - 16.67) does not
        s = summary([("lamp", 2)], CATALOG, coupon_pct=25)
        self.assertEqual(s["discount"], D("16.67"))
        self.assertEqual(s["net"], D("49.99"))
        self.assertEqual(s["shipping"], D("5.00"))
        self.assertEqual(s["tax"], D("4.00"))
        self.assertEqual(s["total"], D("58.99"))

    def test_rounding_half_up_in_tax(self):
        s = summary([("cable", 1), ("pen", 1)], CATALOG)   # 2.67 + 1.25 = 3.92
        self.assertEqual(s["subtotal"], D("3.92"))

    def test_place_order_is_atomic(self):
        inv = Inventory({"pen": 10, "book": 1})
        with self.assertRaises(OutOfStock):
            place_order(inv, [("pen", 2), ("book", 5)], CATALOG)
        self.assertEqual(inv.available("pen"), 10)

    def test_place_order_end_to_end(self):
        inv = Inventory({"lamp": 5, "pen": 100})
        s = place_order(inv, [("lamp", 2), ("pen", 4)], CATALOG, coupon_pct=10)
        self.assertEqual(s["subtotal"], D("71.66"))
        self.assertEqual(s["discount"], D("7.17"))
        self.assertEqual(s["shipping"], D("0.00"))
        self.assertEqual(s["total"], D("69.65"))
        self.assertEqual(inv.available("lamp"), 3)
        self.assertEqual(inv.available("pen"), 96)


if __name__ == "__main__":
    unittest.main()
EOF
