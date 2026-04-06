package realquery

import "fmt"

// ecommerceBurst generates the next burst for the e-commerce scenario.
func (e *Engine) ecommerceBurst() *QueryBatch {
	// State machine: products → product detail → cart → checkout
	transitions := []struct {
		page    string
		weight  int
		queries func() []string
	}{
		{"/products", 30, e.browseProducts},
		{"/products/:id", 25, e.viewProduct},
		{"/cart", 15, e.viewCart},
		{"/checkout", 10, e.checkout},
		{"/categories", 10, e.browseCategories},
		{"/orders", 10, e.viewOrders},
	}

	// Weighted random page selection
	total := 0
	for _, t := range transitions {
		total += t.weight
	}
	r := e.rng.Intn(total)
	cumulative := 0
	selected := transitions[0]
	for _, t := range transitions {
		cumulative += t.weight
		if r < cumulative {
			selected = t
			break
		}
	}

	e.state.CurrentPage = selected.page
	queries := selected.queries()
	return &QueryBatch{
		Queries: queries,
		Page:    selected.page,
	}
}

func (e *Engine) browseProducts() []string {
	catID := e.rng.Intn(10) + 1
	limit := 10 + e.rng.Intn(20)
	return []string{
		fmt.Sprintf("SELECT id, name, price, stock FROM products WHERE category_id = %d LIMIT %d", catID, limit),
		fmt.Sprintf("SELECT name FROM categories WHERE id = %d", catID),
		fmt.Sprintf("SELECT count(*) FROM products WHERE category_id = %d", catID),
	}
}

func (e *Engine) viewProduct() []string {
	productID := e.rng.Intn(200) + 1
	e.state.ViewedIDs = append(e.state.ViewedIDs, productID)
	return []string{
		fmt.Sprintf("SELECT * FROM products WHERE id = %d", productID),
		fmt.Sprintf("SELECT r.rating, r.text FROM reviews WHERE product_id = %d LIMIT 5", productID),
		fmt.Sprintf("SELECT id, name, price FROM products WHERE category_id = (SELECT category_id FROM products WHERE id = %d) LIMIT 4", productID),
	}
}

func (e *Engine) viewCart() []string {
	return []string{
		"SELECT ci.id, p.name, p.price, ci.quantity FROM cart_items ci JOIN products p ON ci.product_id = p.id WHERE ci.user_id = 1",
		"SELECT count(*) FROM cart_items WHERE user_id = 1",
		"SELECT SUM(p.price * ci.quantity) FROM cart_items ci JOIN products p ON ci.product_id = p.id WHERE ci.user_id = 1",
	}
}

func (e *Engine) addToCart() []string {
	productID := e.rng.Intn(200) + 1
	if len(e.state.ViewedIDs) > 0 {
		productID = e.state.ViewedIDs[e.rng.Intn(len(e.state.ViewedIDs))]
	}
	e.state.CartItems = append(e.state.CartItems, productID)
	qty := e.rng.Intn(3) + 1
	return []string{
		fmt.Sprintf("INSERT INTO cart_items (user_id, product_id, quantity) VALUES (1, %d, %d)", productID, qty),
		"SELECT count(*) FROM cart_items WHERE user_id = 1",
	}
}

func (e *Engine) checkout() []string {
	return []string{
		"SELECT ci.id, p.name, p.price, ci.quantity FROM cart_items ci JOIN products p ON ci.product_id = p.id WHERE ci.user_id = 1",
		"INSERT INTO orders (user_id, total, status) VALUES (1, 0, 'pending')",
		"DELETE FROM cart_items WHERE user_id = 1",
		"SELECT count(*) FROM orders WHERE user_id = 1",
	}
}

func (e *Engine) browseCategories() []string {
	return []string{
		"SELECT id, name FROM categories",
		"SELECT c.id, c.name, count(p.id) FROM categories c LEFT JOIN products p ON p.category_id = c.id GROUP BY c.id, c.name",
	}
}

func (e *Engine) viewOrders() []string {
	return []string{
		"SELECT id, total, status, created_at FROM orders WHERE user_id = 1 ORDER BY created_at DESC LIMIT 10",
		"SELECT count(*) FROM orders WHERE user_id = 1",
	}
}
