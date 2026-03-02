# 20 — Collaborative Recipe Kitchen

Same agents as [11-recipe-kitchen](../11-recipe-kitchen/), but using `discuss` mode. Chef, nutritionist, and plater self-organize.

**Compare with:** [11-recipe-kitchen](../11-recipe-kitchen/) (explicit chain)

## Agents

- **chef** — creates detailed recipes
- **nutritionist** — analyzes nutritional content
- **plater** — designs plating guides

## Usage

```bash
swarm up
swarm submit --mode discuss "create a modern brunch take on eggs benedict — I want the full recipe with nutritional breakdown and restaurant-quality plating instructions"
swarm history
swarm down
```

## How It Works

All three agents should see relevance: the task mentions recipe creation, nutrition, and plating. Unlike the chain version (11), where the nutritionist only sees the chef's recipe, here all three work from the original request.

The plater might produce a more creative result since it's not constrained by the chef's specific recipe — it can suggest plating based on the concept rather than exact ingredients.

## Pattern: Independent Expertise

```
         ┌─ chef         [EXECUTE — recipe creation]
discuss ─┼─ nutritionist [EXECUTE — nutritional analysis]
         └─ plater       [EXECUTE — plating design]
```

Good candidate for collaboration: all three are domain experts who can work independently from the same brief.
