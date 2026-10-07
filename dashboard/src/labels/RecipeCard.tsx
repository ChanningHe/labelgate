import type { CSSProperties } from 'react';
import type { LabelRecipe } from './catalog';
import { CodeBlock } from './CodeBlock';
import classes from './LabelsPage.module.css';

export function RecipeCard({ recipe, order = 0 }: { recipe: LabelRecipe; order?: number }) {
  return (
    <article className={`lg-panel ${classes.recipe}`} style={{ '--i': order } as CSSProperties}>
      <h3>{recipe.title}</h3>
      <p>{recipe.description}</p>
      <CodeBlock lines={recipe.lines} image={recipe.image} />
    </article>
  );
}
